// Comando bench_reader mede a vazão de leitura do TraceReader.
//
// Uso: go run ./cmd/bench_reader -trace=data/anonymized_on_hour_sampled_trace.csv
package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"cache-simulator/shared"
)

func main() {
	tracePath := flag.String("trace", "data/anonymized_on_hour_sampled_trace.csv", "caminho do trace CSV")
	flag.Parse()

	start := time.Now()
	out, errCh := shared.IterTrace(*tracePath, -1, -1)
	count := 0
	for range out {
		count++
	}
	if err := <-errCh; err != nil {
		log.Fatalf("ler trace: %v", err)
	}
	elapsed := time.Since(start)
	fmt.Printf("Lidas %d requisições em %v (%.0f req/s)\n", count, elapsed, float64(count)/elapsed.Seconds())
}
