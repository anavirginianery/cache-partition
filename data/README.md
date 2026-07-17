# Dados

## Conteúdo deste diretório

| Arquivo | Versionado | Descrição |
|---|---|---|
| `head_trace.csv` | sim | Amostra de 10 linhas, para inspecionar o formato sem baixar o trace completo |
| `anonymized_on_hour_sampled_trace.csv` | **não** (~1,2 GB) | Trace completo de 1 hora usado no experimento |

O trace completo não é versionado por causa do tamanho. Ele deve ser obtido a
partir do dataset público citado no artigo (`VTEXCacheDataset`) e colocado neste
diretório com o nome `anonymized_on_hour_sampled_trace.csv`, que é o caminho
esperado por `config/experiment.yaml`.

> **Link do dataset:** preencher com a URL/DOI exata do depósito público do
> `VTEXCacheDataset` (a mesma referenciada no `references.bib` do artigo).

## Formato

CSV com cabeçalho, uma requisição por linha:

```text
tenant,product,timestamp,size
e05f6f21...,0c85e96c...,0,519
c102e4ed...,446fbe3c...,0,710
```

| Campo | Tipo | Descrição |
|---|---|---|
| `tenant` | string | Identificador anonimizado do locatário (hash SHA-256) |
| `product` | string | Identificador anonimizado do produto (hash SHA-256) |
| `timestamp` | int | Instante da requisição, **em microssegundos**, relativo ao início do trace |
| `size` | int | Tamanho da resposta em bytes |

**Atenção ao timestamp:** o CSV está em microssegundos. O `shared/trace_reader.go`
converte para segundos na leitura (divide por 1.000.000), então todos os parâmetros
de tempo em `config/experiment.yaml` (`warmup_seconds`, `total_seconds`, janelas do
FairShare) são expressos **em segundos**.

## Caracterização esperada

Ao rodar `cmd/characterize` sobre o trace correto, espere exatamente estes valores.
Se divergirem, o trace não é o mesmo usado no artigo:

| Métrica | Valor |
|---|---|
| Total de requisições | 8.140.207 |
| Tenants distintos | 4.394 |
| Produtos distintos | 5.182.659 |
| Footprint | 4.241.016.319 bytes (~4.044 MB) |
| Tamanho médio do item | 818,31 bytes |
| Taxa de repetição | 0,3633 |
| Duração | 3.599,96 s (~1 h) |

Esses valores estão registrados em `artifacts/v1/workload_profile.json`, gerado
pela execução que produziu os resultados do artigo.

## Artefatos derivados

Ficam em `artifacts/v1/` e não são versionados, porque são reprodutíveis a partir
do trace:

- `artifacts/v1/windows_demand.json` (~28 MB) — curvas de demanda por tenant/janela
  (algoritmo de Mattson), consumido pelos 7 cenários FairShare. Regeneração: ~2 min.
- `artifacts/v1/snapshots/` — estados do simulador pós-warmup, apenas otimização de
  re-execução. A reprodução canônica roda com `-snapshots=false`.
