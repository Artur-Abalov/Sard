// SPDX-License-Identifier: AGPL-3.0-only
// Copyright 2026 Artur Abalov

package redact

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
)

// benchValues: 40 random 12-48 byte secrets and two PEM-sized keys.
func benchValues(r *rand.Rand) [][]byte {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/=@:% "
	gen := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = chars[r.IntN(len(chars))]
		}
		return b
	}
	var vs [][]byte
	for range 40 {
		vs = append(vs, gen(12+r.IntN(37)))
	}
	for range 2 {
		body := gen(1600)
		for i := 64; i < len(body); i += 65 {
			body[i] = '\n'
		}
		vs = append(vs, []byte("-----BEGIN PRIVATE KEY-----\n"+string(body)+"\n-----END PRIVATE KEY-----\n"))
	}
	return vs
}

// benchStream: log-like restic --json lines, one in 50 carrying a secret.
func benchStream(r *rand.Rand, values [][]byte, size int) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		if i%50 == 0 {
			fmt.Fprintf(&b, `{"message_type":"error","error":{"message":"auth %s failed"}}`+"\n", values[r.IntN(len(values))])
			continue
		}
		fmt.Fprintf(&b, `{"message_type":"status","percent_done":0.%04d,"files_done":%d,"current_files":["/var/lib/data/file-%d.dat"]}`+"\n", r.IntN(10000), i, i)
	}
	return b.Bytes()
}

func BenchmarkWrite(b *testing.B) {
	r := rand.New(rand.NewPCG(1, 2))
	values := benchValues(r)
	for _, size := range []int{1 << 20, 8 << 20} {
		data := benchStream(r, values, size)
		for _, chunk := range []int{17, 4096, 64 << 10} {
			b.Run(fmt.Sprintf("%dMiB/chunk=%d", size>>20, chunk), func(b *testing.B) {
				benchWrite(b, values, data, chunk)
			})
		}
	}
}

func benchWrite(b *testing.B, values [][]byte, data []byte, chunk int) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		w, err := New(io.Discard, values)
		if err != nil {
			b.Fatal(err)
		}
		for off := 0; off < len(data); off += chunk {
			if _, err := w.Write(data[off:min(off+chunk, len(data))]); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteAdversarial: values a^k b for k = 1..40 over a stream of
// "a", so every byte sits deep in the automaton and no value ever matches.
func BenchmarkWriteAdversarial(b *testing.B) {
	var values [][]byte
	for k := 1; k <= 40; k++ {
		values = append(values, []byte(strings.Repeat("a", k)+"b"))
	}
	for _, size := range []int{1 << 20, 8 << 20} {
		data := bytes.Repeat([]byte("a"), size)
		b.Run(fmt.Sprintf("%dMiB", size>>20), func(b *testing.B) {
			benchWrite(b, values, data, 4096)
		})
	}
}

func BenchmarkNew(b *testing.B) {
	values := benchValues(rand.New(rand.NewPCG(1, 2)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := New(io.Discard, values); err != nil {
			b.Fatal(err)
		}
	}
}

// TestBenchStreamIsMasked keeps the benchmark honest: its secrets are
// actually found.
func TestBenchStreamIsMasked(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	values := benchValues(r)
	data := benchStream(r, values, 64<<10)
	var out bytes.Buffer
	w, err := New(&out, values)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
	_ = w.Close()
	if n := strings.Count(out.String(), Marker); n != strings.Count(string(data), `"error":{`) {
		t.Fatalf("%d markers for %d secret lines", n, strings.Count(string(data), `"error":{`))
	}
	for _, v := range values {
		if bytes.Contains(out.Bytes(), v) {
			t.Fatal("a value survived")
		}
	}
}
