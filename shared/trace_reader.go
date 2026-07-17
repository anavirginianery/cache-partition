package shared

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// IterTrace abre o trace CSV e retorna um canal de Request lidas em streaming.
// O trace é esperado no formato com header: tenant,product,timestamp,size
//
// Filtros opcionais:
//   - startTs >= 0: pula requests com Timestamp < startTs (não emite)
//   - endTs >= 0: para de emitir quando Timestamp >= endTs e fecha o canal
//
// Use startTs=-1 ou endTs=-1 para desativar o filtro respectivo.
//
// O canal de erro é fechado junto com o canal de requests. Se houver erro
// fatal (arquivo inexistente, parse error), o erro é enviado antes do close.
func IterTrace(path string, startTs, endTs float64) (<-chan Request, <-chan error) {
	out := make(chan Request, 4096)
	errCh := make(chan error, 1)

	go func() {
		defer close(out)
		defer close(errCh)

		f, err := os.Open(path)
		if err != nil {
			errCh <- fmt.Errorf("abrir trace %q: %w", path, err)
			return
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		// Buffer maior para linhas longas (hashes hex podem ser ~64 chars cada
		// + outros campos; folga generosa).
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 8*1024*1024)

		// Skip header
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				errCh <- fmt.Errorf("ler header: %w", err)
			}
			return
		}

		lineNum := 1
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if line == "" {
				continue
			}

			req, err := parseTraceLine(line)
			if err != nil {
				errCh <- fmt.Errorf("linha %d: %w", lineNum, err)
				return
			}

			if startTs >= 0 && req.Timestamp < startTs {
				continue
			}
			if endTs >= 0 && req.Timestamp >= endTs {
				return
			}

			out <- req
		}

		if err := scanner.Err(); err != nil {
			errCh <- fmt.Errorf("scanner: %w", err)
		}
	}()

	return out, errCh
}

// parseTraceLine faz parsing manual de uma linha CSV no formato esperado.
// Mais rápido que encoding/csv para linhas simples sem aspas.
func parseTraceLine(line string) (Request, error) {
	// Encontrar 3 vírgulas (4 campos)
	idx1 := strings.IndexByte(line, ',')
	if idx1 < 0 {
		return Request{}, fmt.Errorf("formato inválido (sem vírgulas): %q", line)
	}
	rest := line[idx1+1:]
	idx2 := strings.IndexByte(rest, ',')
	if idx2 < 0 {
		return Request{}, fmt.Errorf("formato inválido (poucas colunas): %q", line)
	}
	rest2 := rest[idx2+1:]
	idx3 := strings.IndexByte(rest2, ',')
	if idx3 < 0 {
		return Request{}, fmt.Errorf("formato inválido (poucas colunas): %q", line)
	}

	tenant := line[:idx1]
	product := rest[:idx2]
	tsStr := rest2[:idx3]
	sizeStr := rest2[idx3+1:]

	tsRaw, err := strconv.ParseFloat(tsStr, 64)
	if err != nil {
		return Request{}, fmt.Errorf("timestamp inválido %q: %w", tsStr, err)
	}

	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return Request{}, fmt.Errorf("size inválido %q: %w", sizeStr, err)
	}

	// Trace usa timestamps em microssegundos. Convertemos para segundos
	// para alinhar com config (warmup_seconds, etc.) e simplificar leitura.
	ts := tsRaw / 1_000_000.0

	return Request{
		TenantID:  tenant,
		ProductID: product,
		Timestamp: ts,
		Size:      size,
	}, nil
}
