package fairshare

import (
	"sort"
)

// UsefulDemandForMaxHR retorna o menor tamanho de partição que atinge o maior
// HR observado na curva. Esse valor é a demanda útil escalar do tenant: acima
// dele, a curva não promete ganho adicional de HR.
func UsefulDemandForMaxHR(curve []HRPoint) int64 {
	if len(curve) == 0 {
		return 0
	}
	maxHR := curve[0].HR
	for _, p := range curve[1:] {
		if p.HR > maxHR {
			maxHR = p.HR
		}
	}
	for _, p := range curve {
		if p.HR >= maxHR {
			return p.SizeBytes
		}
	}
	return curve[len(curve)-1].SizeBytes
}

// MaxMinAllocateDemand distribui capacidade entre demandas escalares usando
// progressive filling. Se capacity > soma(demandas), redistribui o excedente
// igualmente entre todos os tenants, como no algoritmo Max-Min LSD original.
func MaxMinAllocateDemand(demands map[string]int64, capacity int64) map[string]int64 {
	return maxMinAllocateDemand(demands, capacity, true)
}

func maxMinAllocateDemand(demands map[string]int64, capacity int64, redistributeExtra bool) map[string]int64 {
	out := make(map[string]int64, len(demands))
	if capacity <= 0 || len(demands) == 0 {
		return out
	}

	type tenantDemand struct {
		tenant string
		demand int64
	}
	items := make([]tenantDemand, 0, len(demands))
	for tenant, demand := range demands {
		if demand < 0 {
			demand = 0
		}
		out[tenant] = 0
		items = append(items, tenantDemand{tenant: tenant, demand: demand})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].demand != items[j].demand {
			return items[i].demand < items[j].demand
		}
		return items[i].tenant < items[j].tenant
	})

	capacityRemaining := capacity
	for i, item := range items {
		remainingTenants := int64(len(items) - i)
		if remainingTenants <= 0 || capacityRemaining <= 0 {
			break
		}
		share := capacityRemaining / remainingTenants
		allocation := minInt64(share, item.demand)
		if i == len(items)-1 && item.demand >= capacityRemaining {
			allocation = capacityRemaining
		}
		out[item.tenant] = allocation
		capacityRemaining -= allocation
	}

	if redistributeExtra && capacityRemaining > 0 {
		add := capacityRemaining / int64(len(items))
		if add > 0 {
			for _, item := range items {
				out[item.tenant] += add
			}
		}
	}
	return out
}

// MaxMinAllocateByUsefulDemand calcula demand_i a partir da curva de cada
// tenant e aplica MaxMinAllocateDemand. Quando floorBytes > 0, tenta garantir
// um piso inicial por tenant limitado pela demanda útil; depois, se ainda
// houver capacidade, a fase final pode redistribuir excedente acima da demanda.
func MaxMinAllocateByUsefulDemand(curves map[string][]HRPoint, capacity, floorBytes int64) map[string]int64 {
	demands := make(map[string]int64, len(curves))
	for tenant, curve := range curves {
		demands[tenant] = UsefulDemandForMaxHR(curve)
	}
	if floorBytes <= 0 {
		return MaxMinAllocateDemand(demands, capacity)
	}

	floorDemands := make(map[string]int64, len(demands))
	for tenant, demand := range demands {
		floorDemands[tenant] = minInt64(floorBytes, demand)
	}
	out := maxMinAllocateDemand(floorDemands, capacity, false)

	var used int64
	for _, allocation := range out {
		used += allocation
	}
	remaining := capacity - used
	if remaining <= 0 {
		return out
	}

	residualDemands := make(map[string]int64, len(demands))
	for tenant, demand := range demands {
		residual := demand - out[tenant]
		if residual < 0 {
			residual = 0
		}
		residualDemands[tenant] = residual
	}
	extra := MaxMinAllocateDemand(residualDemands, remaining)
	for tenant, allocation := range extra {
		out[tenant] += allocation
	}
	return out
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// MaxMinAllocate distribui a capacidade total entre tenants, maximizando
// o Hit Ratio mínimo entre eles. Algoritmo "waterfilling":
//
//  1. Começa com HR alvo = 0 e aumenta gradualmente.
//  2. Para cada HR alvo, calcula quanto cada tenant precisaria para
//     atingir esse HR (via SizeForHR sobre sua curva).
//  3. Avança o HR alvo enquanto a demanda total ≤ capacidade.
//  4. Quando a demanda excede capacidade, distribui o restante
//     proporcionalmente ao excesso (delta) que cada tenant ainda
//     "demanda" para o próximo nível de HR.
//
// Tenants sem curva (não ativos na janela) não recebem alocação aqui;
// o caller deve dar um floor mínimo (ex: 1 item) ou ignorar.
//
// Retorna map[tenantID] → bytes_alocados. A soma das alocações é ≤ capacity.
func MaxMinAllocate(curves map[string][]HRPoint, capacity int64) map[string]int64 {
	allocated := make(map[string]int64, len(curves))
	if capacity <= 0 || len(curves) == 0 {
		return allocated
	}

	// Floor inicial: tenta dar 1 byte para cada tenant para evitar zero-share.
	// Mas o caller (FairShare) é quem decide piso real. Aqui começamos em 0.
	for t := range curves {
		allocated[t] = 0
	}

	// HR alvo aumenta em passos finos. 1000 passos = resolução ~0.1% do HR.
	const numSteps = 1000
	step := 1.0 / float64(numSteps)

	for s := 1; s <= numSteps; s++ {
		hrTarget := float64(s) * step

		// Calcular demanda total para hrTarget.
		var demandTotal int64
		demands := make(map[string]int64, len(curves))
		for t, curve := range curves {
			needed := SizeForHR(curve, hrTarget)
			if needed > allocated[t] {
				delta := needed - allocated[t]
				demands[t] = delta
				demandTotal += delta
			}
		}
		if demandTotal == 0 {
			continue // ninguém precisa de mais para atingir hrTarget
		}

		// Calcular quanto resta da capacidade.
		var totalAllocated int64
		for _, v := range allocated {
			totalAllocated += v
		}
		remaining := capacity - totalAllocated
		if remaining <= 0 {
			break
		}

		if demandTotal <= remaining {
			// Todos podem subir para hrTarget completo.
			for t, d := range demands {
				allocated[t] += d
			}
		} else {
			// Distribuir proporcionalmente ao demand individual.
			ratio := float64(remaining) / float64(demandTotal)
			for t, d := range demands {
				allocated[t] += int64(float64(d) * ratio)
			}
			break // capacidade esgotada
		}
	}

	return allocated
}

// MaxMinAllocateWithFloor é variante que garante que cada tenant ativo
// receba pelo menos `floorBytes`. Útil para evitar tenants com 0 cache
// (que não conseguem nem armazenar 1 item).
//
// Se a soma dos floors > capacity, o caller deve aceitar que nem todos
// terão o piso (essa função distribuirá igualmente nesse caso).
func MaxMinAllocateWithFloor(curves map[string][]HRPoint, capacity, floorBytes int64) map[string]int64 {
	if floorBytes <= 0 {
		return MaxMinAllocate(curves, capacity)
	}
	n := int64(len(curves))
	if n == 0 {
		return map[string]int64{}
	}
	totalFloor := floorBytes * n

	if totalFloor >= capacity {
		// Capacidade insuficiente para os pisos; distribuir igualmente.
		share := capacity / n
		out := make(map[string]int64, len(curves))
		for t := range curves {
			out[t] = share
		}
		return out
	}

	// Aplicar piso, depois Max-Min sobre o restante.
	allocated := make(map[string]int64, len(curves))
	for t := range curves {
		allocated[t] = floorBytes
	}
	remaining := capacity - totalFloor

	// Reduzir todas as curvas para refletir já-alocado: chamamos MaxMinAllocate
	// com capacidade = remaining e depois SOMAMOS ao piso. Mas as curvas
	// não devem ser modificadas — em vez disso, o waterfilling acima
	// soma sobre allocated[t] que já tem floorBytes. Então:
	extra := MaxMinAllocateWithFloorInternal(curves, remaining, allocated)
	for t, v := range extra {
		allocated[t] += v
	}
	return allocated
}

// MaxMinAllocateWithFloorInternal é o waterfilling considerando alocação
// já existente em initial[]. Retorna o INCREMENTO sobre initial.
func MaxMinAllocateWithFloorInternal(curves map[string][]HRPoint, capacity int64, initial map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(curves))
	if capacity <= 0 {
		return out
	}
	current := make(map[string]int64, len(curves))
	for t := range curves {
		current[t] = initial[t]
	}

	const numSteps = 1000
	step := 1.0 / float64(numSteps)
	var totalIncrement int64

	for s := 1; s <= numSteps; s++ {
		hrTarget := float64(s) * step
		var demandTotal int64
		demands := make(map[string]int64, len(curves))
		for t, curve := range curves {
			needed := SizeForHR(curve, hrTarget)
			if needed > current[t] {
				delta := needed - current[t]
				demands[t] = delta
				demandTotal += delta
			}
		}
		if demandTotal == 0 {
			continue
		}
		remaining := capacity - totalIncrement
		if remaining <= 0 {
			break
		}
		if demandTotal <= remaining {
			for t, d := range demands {
				current[t] += d
				out[t] += d
				totalIncrement += d
			}
		} else {
			ratio := float64(remaining) / float64(demandTotal)
			for t, d := range demands {
				inc := int64(float64(d) * ratio)
				current[t] += inc
				out[t] += inc
			}
			break
		}
	}

	return out
}

// SortedTenantsByHR retorna a lista de tenants ordenada por HR ascendente
// para a alocação dada. Útil para análise/debug.
func SortedTenantsByHR(curves map[string][]HRPoint, allocation map[string]int64) []string {
	type pair struct {
		Tenant string
		HR     float64
	}
	pairs := make([]pair, 0, len(allocation))
	for t, size := range allocation {
		hr := InterpolateHR(curves[t], size)
		pairs = append(pairs, pair{t, hr})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].HR < pairs[j].HR
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.Tenant
	}
	return out
}
