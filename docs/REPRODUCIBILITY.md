# Guia de Reprodução

Reproduz o protocolo descrito no artigo: trace de 1 hora, warmup de 10 minutos,
medição de 50 minutos, sete níveis de capacidade e três políticas
(`no_partition`, `fairshare`, `memshare`) — 21 cenários no total.

Para rodar tudo de uma vez, use `./scripts/run_all.sh`. Este documento detalha
cada etapa e a justificativa dos parâmetros, para quem precisa entender ou
adaptar o experimento.

## 1. Ambiente

Todos os comandos são executados a partir da raiz do repositório.

```bash
go version      # >= 1.21 (validado com 1.26.4)
python3 --version  # >= 3.10
pip install -r requirements.txt
```

Ambiente de referência da execução reportada no artigo:

| Item | Valor |
|---|---|
| CPU | Intel Core i5-11300H @ 3.10 GHz (8 threads) |
| RAM | 8 GB |
| SO | Linux Mint 21.2 (kernel 5.15) |
| Go | 1.26.4 |
| Python | 3.10.12 |

Requisitos mínimos: ~8 GB de RAM (o algoritmo de Mattson mantém pilhas por tenant)
e ~6 GB de disco livre.

## 2. Obter o trace

O trace vem do dataset público
[ufcg-lsd/vtex-ufcg-cache-dataset](https://github.com/ufcg-lsd/vtex-ufcg-cache-dataset)
e deve ficar em `data/anonymized_on_hour_sampled_trace.csv`, que é o caminho
esperado por `config/experiment.yaml`. Ver [`data/README.md`](../data/README.md)
para o formato e a caracterização esperada.

Formato: `tenant,product,timestamp,size`, com `timestamp` **em microssegundos**
(convertido para segundos na leitura).

## 3. Configuração

`config/experiment.yaml` é a **única** configuração do experimento:

```text
warmup_seconds = 600
total_seconds  = 3600
capacities_pct = 5, 10, 15, 20, 25, 30, 50

memshare_reserved_proportion = 0.5
memshare_credit_bytes        = auto   # = avg_item_size
memshare_seed                = 42

fairshare_window_seconds = 600
fairshare_slide_seconds  = 600
fairshare_floor_bytes    = auto       # = avg_item_size
```

**Por que `window=600` e `slide=600`.** O FairShare original usa janela de 60 min,
calibrada para traces de 10 h. Com 1 h de trace, isso produziria uma única decisão
de alocação. A adaptação decide a cada 10 min usando os 10 min anteriores já
fechados: em `t=600` usa `[0,600)`; em `t=1200` usa `[600,1200)`. Isso equipara o
FairShare ao Memshare em nível de informação disponível (apenas dados passados),
tornando a comparação livre de viés de informação privilegiada.

**Por que 50% reservado no Memshare.** Segue o valor empregado por Nery e Silva,
para avaliar a política em condições próximas às já estudadas.

## 4. Executar

### 4.1 Testes

```bash
go test ./...
```

Todos os pacotes devem passar.

### 4.2 Caracterizar o workload

Gera `footprint_bytes` (base das capacidades), `avg_item_size` (crédito do Memshare
e floor do FairShare) e `num_tenants`.

```bash
go run ./cmd/characterize \
  -trace=data/anonymized_on_hour_sampled_trace.csv \
  -out=artifacts/v1/workload_profile.json
```

Valores esperados — se divergirem, o trace não é o mesmo:

```text
Total requests:      8140207
Tenants ativos:      4394
Items distintos:     5182659
Footprint:           4241016319 bytes (~4044,55 MB)
Tamanho médio item:  818,31 bytes
Taxa de repetição:   0,3633
Duração:             3599,96s
```

### 4.3 Precomputar a demanda do FairShare

Curvas HR por tenant e janela via *stack distance* (Mattson). Roda uma vez e é
reaproveitada pelos 7 cenários FairShare. Leva ~2 min.

```bash
go run ./cmd/precompute_demand \
  -trace=data/anonymized_on_hour_sampled_trace.csv \
  -window=600 -slide=600 -duration=3600 \
  -out=artifacts/v1/windows_demand.json
```

Com `duration=3600, window=600, slide=600`, espere **6 janelas**:
`[0,600)`, `[600,1200)`, `[1200,1800)`, `[1800,2400)`, `[2400,3000)`, `[3000,3600)`.

### 4.4 Rodar os 21 cenários

```bash
go run . -config=config/experiment.yaml -snapshots=false
```

`-snapshots=false` força o warmup completo em cada cenário, evitando reaproveitar
estado salvo de outra configuração. É a forma canônica de reprodução (~7 min).

Snapshots pós-warmup são apenas uma otimização de re-execução. Para usá-los,
garanta que `artifacts/v1/snapshots/` esteja **vazio** antes e omita a flag.

Saídas: `artifacts/v1/results/E1.json` … `E21.json`.

```text
E1-E7   = no_partition   nas capacidades 5,10,15,20,25,30,50
E8-E14  = fairshare      nas capacidades 5,10,15,20,25,30,50
E15-E21 = memshare       nas capacidades 5,10,15,20,25,30,50
```

### 4.5 Sanity checks

```bash
go run ./cmd/sanity -results=artifacts/v1/results
```

- `PASS` — comportamento esperado.
- `WARN` — possível, mas merece inspeção.
- `FAIL` — revisar a execução antes de usar os resultados. Sai com código 1.

O FairShare **deve** apresentar interferência exatamente zero: suas partições são
rígidas e independentes, então toda evicção é auto-evicção.

### 4.6 Agregar

```bash
go run ./cmd/aggregate -results=artifacts/v1/results -out=artifacts/v1/analysis
```

| Arquivo | Conteúdo |
|---|---|
| `agg_global.csv` | Uma linha por cenário: HR global, hits, misses, interferência, wall time |
| `agg_per_tenant.csv` | Uma linha por (cenário, tenant): HR e interferência |
| `agg_comparisons.csv` | Por (capacidade, política): Δ HR e % de tenants melhorados vs. No Partition |

### 4.7 Figuras

```bash
python3 analysis/visualization.py \
  --input artifacts/v1/analysis --output artifacts/v1/analysis
```

| Figura | Uso no artigo |
|---|---|
| `plot_hr_vs_capacity.png` | Hit Ratio global × capacidade (H1) |
| `plot_hr_change_stacked.png` | Fração de tenants melhorados/mantidos/piorados (H3) |
| `plot_boxplot_interference.png` | Distribuição da interferência por tenant (H2) |
| `plot_cdf_hr_per_tenant.png` | CDF do HR por tenant (material complementar) |
| `plot_pct_improved.png` | % de tenants melhorados (material complementar) |

## 5. Verificar a reprodução

A simulação é determinística: mesmo trace + mesma config ⇒ mesmos números.
Confira contra `artifacts/v1/analysis/agg_global.csv`:

| Cenário | Política | Cap. | HR global esperado |
|---|---|---|---|
| E1 | no_partition | 5% | 0,123940 |
| E7 | no_partition | 50% | 0,357583 |
| E8 | fairshare | 5% | 0,094405 |
| E14 | fairshare | 50% | 0,288002 |
| E15 | memshare | 5% | 0,103973 |
| E21 | memshare | 50% | 0,347703 |

Só o `wall_time_seconds` deve variar, por depender do hardware.

Verificações qualitativas independentes de hardware:

- HR global cresce monotonicamente com a capacidade, em cada política.
- No Partition tem o maior HR global em todas as capacidades.
- FairShare tem `system_interference = 0` nos 7 cenários.
- `global_hits + global_misses` = total de requisições em `[600, 3600)`.

## 6. Notas metodológicas

- Warmup `[0,600)`; medição `[600,3600)`. Métricas não são coletadas no warmup.
- No FairShare, a primeira requisição com `t >= 600` aciona a primeira alocação,
  usando a janela `[0,600)`.
- Tenants que ainda não receberam partição do alocador FairShare geram miss até
  aparecerem numa janela de demanda. Isso é parte da limitação da política
  discutida no artigo, não um bug.
- O Memshare divide `P` igualmente entre os tenants e usa crédito automático igual
  ao tamanho médio dos itens distintos.
- Interferência por tenant:

```text
interference = interference_returns / cross_tenant_evictions
```

  onde `interference_returns` é o número de itens removidos por outro tenant que
  voltaram a ser requisitados, e `cross_tenant_evictions` é o total de itens
  removidos por outros tenants. Tenants sem evicções cross-tenant não contribuem.

## 7. Artefatos a preservar

Para arquivar uma reprodução:

```text
config/experiment.yaml
artifacts/v1/workload_profile.json
artifacts/v1/results/*.json
artifacts/v1/analysis/*.csv
artifacts/v1/analysis/*.png
```

Registre também `go version` e `python3 --version`.
