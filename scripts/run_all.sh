#!/usr/bin/env bash
#
# Pipeline completo de reprodução dos experimentos do artigo.
#
# Executa, em ordem:
#   1. testes            2. caracterização do workload
#   3. demanda FairShare 4. os 21 cenários
#   5. agregação         6. sanity checks         7. figuras
#
# Uso (a partir da raiz do repositório):
#   ./scripts/run_all.sh
#
# Pré-requisitos: Go >= 1.21, Python >= 3.10, e o trace em
# data/anonymized_on_hour_sampled_trace.csv (ver data/README.md).

set -euo pipefail

cd "$(dirname "$0")/.."

CONFIG="config/experiment.yaml"
TRACE="data/anonymized_on_hour_sampled_trace.csv"
OUT="artifacts/v1"

if [[ ! -f "$TRACE" ]]; then
  echo "ERRO: trace não encontrado em $TRACE" >&2
  echo "Veja data/README.md para instruções de obtenção." >&2
  exit 1
fi

echo "== Versões =="
go version
python3 --version

mkdir -p "$OUT/results" "$OUT/snapshots" "$OUT/analysis"

echo
echo "== 1/7 Testes =="
go test ./...

echo
echo "== 2/7 Caracterização do workload =="
go run ./cmd/characterize -trace="$TRACE" -out="$OUT/workload_profile.json"

echo
echo "== 3/7 Demanda do FairShare (Mattson, ~2 min) =="
go run ./cmd/precompute_demand \
  -trace="$TRACE" \
  -window=600 -slide=600 -duration=3600 \
  -out="$OUT/windows_demand.json"

echo
echo "== 4/7 Os 21 cenários (~5 min) =="
# -snapshots=false garante warmup limpo, sem reaproveitar estado de outra config.
go run . -config="$CONFIG" -snapshots=false

echo
echo "== 5/7 Agregação =="
go run ./cmd/aggregate -results="$OUT/results" -out="$OUT/analysis"

echo
echo "== 6/7 Sanity checks =="
go run ./cmd/sanity -results="$OUT/results"

echo
echo "== 7/7 Figuras =="
python3 analysis/visualization.py --input "$OUT/analysis" --output "$OUT/analysis"

echo
echo "== Concluído =="
echo "Resultados:  $OUT/results/E{1..21}.json"
echo "Agregados:   $OUT/analysis/agg_*.csv"
echo "Figuras:     $OUT/analysis/plot_*.png"
