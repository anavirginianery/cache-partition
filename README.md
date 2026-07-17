# Avaliação Comparativa de Políticas de Particionamento de Cache Multi-Locatário

Kit de reprodução do artigo *"Avaliação Comparativa de Políticas de Particionamento
de Cache Multi-Locatário"* (Ana Virgínia de Souza Nery, UFCG).

Simulador em Go que compara três políticas de gerenciamento de cache multi-locatário
sobre um trace real de uma plataforma de comércio eletrônico, em 21 cenários
(3 políticas × 7 níveis de capacidade):

| Política | Descrição |
|---|---|
| **No Partition** | LRU global compartilhado, sem isolamento. É o *baseline*. |
| **FairShare Max-Min** | Partições lógicas rígidas por tenant, dimensionadas por Max-Min Fairness sobre curvas de *stack distance* (Mattson). |
| **Memshare** | Particionamento dinâmico e reativo, com *shadow queues* e realocação por crédito. |

Métricas avaliadas: **Hit Ratio** (global e por tenant) e **interferência** entre
locatários.

## Estrutura

```
.
├── shared/              # Tipos, leitor de trace, LRU, coletor de métricas
├── partition/           # Política No Partition (LRU compartilhado)
├── memshare/            # Política Memshare (shadow queues + need-heap)
├── fairshare/           # Política FairShare (Mattson + Max-Min + partições)
├── config/
│   ├── config.go        # Parser do YAML
│   └── experiment.yaml  # ÚNICA configuração do experimento do artigo
├── cmd/
│   ├── characterize/    # Caracteriza o workload -> workload_profile.json
│   ├── precompute_demand/ # Curvas de demanda do FairShare -> windows_demand.json
│   ├── aggregate/       # Consolida os 21 JSONs -> agg_*.csv
│   ├── sanity/          # Validações automáticas dos resultados
│   └── bench_reader/    # Benchmark de vazão do leitor (utilitário)
├── analysis/
│   └── visualization.py # Gera as figuras do artigo
├── data/                # Trace e instruções de obtenção (ver data/README.md)
├── scripts/
│   └── run_all.sh       # Pipeline completo, ponta a ponta
├── artifacts/v1/        # Resultados da execução reportada no artigo
│   ├── workload_profile.json
│   ├── results/         # E1.json .. E21.json
│   └── analysis/        # agg_*.csv + plot_*.png
├── docs/
│   ├── REPRODUCIBILITY.md            # Guia detalhado de reprodução
│   └── design_experimental_cache.docx # Design experimental original
├── run_experiments.go   # Orquestrador dos 21 cenários
├── requirements.txt     # Dependências Python
└── go.mod
```

## Requisitos

- **Go** ≥ 1.21 (desenvolvido e validado com 1.26.4)
- **Python** ≥ 3.10 com `pandas`, `matplotlib`, `numpy` (`requirements.txt`)
- **~8 GB de RAM** e **~6 GB de disco** (trace de 1,2 GB + artefatos)
- O **trace**, obtido do dataset público
  [ufcg-lsd/vtex-ufcg-cache-dataset](https://github.com/ufcg-lsd/vtex-ufcg-cache-dataset)
  e colocado em `data/anonymized_on_hour_sampled_trace.csv`. Não é versionado aqui
  por causa do tamanho — ver [`data/README.md`](data/README.md).

## Reprodução

### Caminho rápido

```bash
pip install -r requirements.txt
./scripts/run_all.sh
```

`scripts/run_all.sh` **reproduz exatamente o experimento do artigo**, e nada além
disso: fixa a configuração em `config/experiment.yaml`, roda o pipeline inteiro na
ordem correta (testes → caracterização → demanda do FairShare → 21 cenários →
agregação → sanity checks → figuras) e força `-snapshots=false` para garantir
warmup limpo. Não é um script genérico: para variar parâmetros, veja o playbook.
Tempo total: **~10 min** num Intel i5-11300H (8 threads, 8 GB RAM).

### Caminho detalhado — o playbook

**Recomendamos acompanhar a reprodução por
[`docs/REPRODUCIBILITY.md`](docs/REPRODUCIBILITY.md)**, mesmo usando o script. O
playbook é o documento de referência da reprodução e traz o que o script não
mostra:

- o **ambiente de referência** exato (CPU, RAM, SO, versões de Go e Python);
- cada etapa isolada, com seus comandos e flags;
- a **justificativa de cada parâmetro** (por que `window=600`/`slide=600`, por que
  50% reservado no Memshare, por que `-snapshots=false`);
- os **valores esperados** para conferir a caracterização do trace e o Hit Ratio de
  cada cenário — a simulação é determinística, então os números devem bater;
- como interpretar os sanity checks e o que é limitação conhecida da política, não bug.

Se algum número divergir, é o playbook que diz onde olhar.

## Mapeamento dos cenários

Capacidades em % do *footprint* (4.241.016.319 bytes), na ordem 5, 10, 15, 20, 25, 30, 50:

| Cenários | Política |
|---|---|
| E1–E7 | `no_partition` |
| E8–E14 | `fairshare` |
| E15–E21 | `memshare` |

## Resultados principais

Da execução em `artifacts/v1/` (Hit Ratio global; Δ em pontos percentuais vs. baseline):

| Capacidade | No Partition | FairShare | Δ | Memshare | Δ |
|---|---|---|---|---|---|
| 5% | 12,39% | 9,44% | −2,95 | 10,40% | −2,00 |
| 10% | 17,02% | 14,41% | −2,61 | 14,44% | −2,58 |
| 15% | 21,19% | 19,13% | −2,05 | 18,00% | −3,19 |
| 20% | 24,44% | 22,95% | −1,50 | 21,38% | −3,06 |
| 25% | 27,32% | 25,76% | −1,56 | 24,41% | −2,91 |
| 30% | 29,63% | 26,88% | −2,76 | 27,16% | −2,48 |
| 50% | 35,76% | 28,80% | **−6,96** | 34,77% | **−0,99** |

Em síntese: o No Partition tem o maior Hit Ratio global em todas as capacidades,
mas também a maior interferência. O FairShare zera a interferência
(`I_i = 0` para todo tenant), ao custo de degradar a maioria dos locatários e de um
colapso de Hit Ratio no sobre-provisionamento. O Memshare oferece o melhor
equilíbrio: melhora 72–78% dos locatários e reduz a interferência média de 0,104
para 0,006 (em 50%), com perda mínima de Hit Ratio.

## Notas metodológicas

- **Timestamps em microssegundos.** O trace usa µs; `shared/trace_reader.go`
  converte para segundos na leitura. Toda a configuração é em segundos.
- **Warmup `[0, 600)`, medição `[600, 3600)`.** Métricas não são coletadas durante
  o warmup. O cache começa vazio para todas as políticas, que processam as mesmas
  requisições na mesma ordem.
- **FairShare reativo.** `window=600, slide=600`: a cada 10 min a alocação é
  decidida com a janela anterior já fechada. Equipara o nível de informação ao do
  Memshare (apenas dados passados), evitando viés de informação privilegiada.
- **Modelo de evicção.** Per-request síncrono: a cada miss que precisa de espaço,
  `EvictLRU()` roda em laço até caber. É mais simples que o modelo de
  segmento/cleaner do paper original do Memshare, e adequado à simulação acadêmica.
- **Determinismo.** O Memshare usa `memshare_seed: 42` para a escolha aleatória do
  locatário doador de crédito.

## Licença

Código sob licença MIT (ver `LICENSE`). O trace é derivado do dataset público
[ufcg-lsd/vtex-ufcg-cache-dataset](https://github.com/ufcg-lsd/vtex-ufcg-cache-dataset)
e está sujeito aos termos do depósito original.
