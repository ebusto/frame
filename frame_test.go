package frame

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"sync"
	"testing"

	randc "crypto/rand"
	randm "math/rand"

	"github.com/ebusto/jitter"
	"github.com/ebusto/mux"
)

func TestFrame(t *testing.T) {
	a, b := net.Pipe()

	fa := New(jitter.New(a, nil), nil)
	fb := New(jitter.New(b, nil), nil)

	ok := make(chan int64)

	max := int64(100)

	add := func(name string, s io.ReadWriter) {
		var n int64

		seen := make(map[int64]bool)

		t.Logf("[%s] writing %d\n", name, n)

		if err := binary.Write(s, binary.LittleEndian, n); err != nil {
			panic(err)
		}

		for {
			if err := binary.Read(s, binary.LittleEndian, &n); err != nil {
				panic(err)
			}

			t.Logf("[%s] read %d, sending %d\n", name, n, n+1)

			if _, ok := seen[n]; ok {
				t.Fatalf("[%s] already seen %d\n", n)
			}

			seen[n] = true

			if err := binary.Write(s, binary.LittleEndian, n+1); err != nil {
				panic(err)
			}

			if n == max {
				break
			}
		}

		ok <- n
		return
	}

	go add("A", fa)
	go add("B", fb)

	if n := <-ok; n != max {
		t.Errorf("[1] n = %d, expected %d", n, max)
	}

	if n := <-ok; n != max {
		t.Errorf("[2] n = %d, expected %d", n, max)
	}
}

func TestFrameMux(t *testing.T) {
	a, b := net.Pipe()

	ma := mux.New(New(jitter.New(a, nil), nil))
	mb := mux.New(New(jitter.New(b, nil), nil))

	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)

		go testFrameMux(t, &wg, byte(i), ma, mb)
	}

	wg.Wait()
}

func testFrameMux(t *testing.T, wg *sync.WaitGroup, id byte, ma *mux.Mux, mb *mux.Mux) {
	sa := ma.Stream(id)
	sb := mb.Stream(id)

	src := make([]byte, 10000)

	if _, err := randc.Read(src); err != nil {
		t.Fatal(err)
	}

	ok := make(chan bool)

	go func() {
		i := 0

		for i < len(src) {
			l := i
			h := i + randm.Intn(len(src)-i) + 1

			i = h

			n, err := sa.Write(src[l:h])

			if err != nil {
				t.Fatal(err)
			}

			t.Logf("[%d] wrote %d [%d], total %d\n", id, n, h-l, i)
		}

		t.Logf("[%d] write done\n", id)

		ok <- true
	}()

	var dst []byte

	go func() {
		buf := make([]byte, 16384)

		for len(dst) != len(src) {
			h := randm.Intn(len(buf))

			n, err := sb.Read(buf[0:h])

			if err != nil {
				t.Fatal(err)
			}

			dst = append(dst, buf[0:n]...)

			t.Logf("[%d] read %d, total %d\n", id, n, len(dst))
		}

		t.Logf("[%d] read done\n", id)

		ok <- true
	}()

	<-ok
	<-ok

	if !bytes.Equal(src, dst) {
		t.Errorf("[%d] mismatch", id)
	}

	wg.Done()
}
