package frame

import (
	"bytes"
	"encoding/binary"
	"errors"
	//"fmt"
	"hash/crc32"
	"io"
	"time"
)

var (
	ErrChecksum     = errors.New("checksum mismatch")
	ErrInvalidStart = errors.New("invalid start byte")
)

const (
	start byte = 0xEB
)

var (
	order = binary.BigEndian
)

type Config struct {
	MTU   int
	Retry time.Duration
}

type Conn struct {
	cn io.ReadWriter
	cf *Config

	fr chan *Frame
	fw chan *Frame

	cr chan io.Reader
	cw chan io.Writer
}

type Frame struct {
	id   byte
	ack  byte
	size byte
	data []byte
}

var defaultConfig = &Config{
	MTU:   200,
	Retry: time.Millisecond * 10,
}

func New(cn io.ReadWriter, cf *Config) *Conn {
	if cf == nil {
		cf = defaultConfig
	}

	c := &Conn{
		cn: cn,
		cf: cf,
		cr: make(chan io.Reader),
		cw: make(chan io.Writer),
		fr: make(chan *Frame),
		fw: make(chan *Frame),
	}

	go c.recv()
	go c.send()
	go c.state()

	return c
}

//
// c.recv -> c.state -> c.send
//
func (c *Conn) recv() {
	for {
		f := &Frame{}

		if err := f.Decode(c.cn); err != nil {
			continue
		}

		c.fr <- f
	}
}

func (c *Conn) send() {
	for f := range c.fw {
		if err := f.Encode(c.cn); err != nil {
			//fmt.Printf("encode: %s\n", err)
		}
	}
}

func (c *Conn) state() {
	sn := make(chan byte)

	go func() {
		for n := byte(1); ; n++ {
			if n == 0 {
				continue
			}

			sn <- n
		}
	}()

	br := new(bytes.Buffer)
	bw := new(bytes.Buffer)

	fb := make([]byte, c.cf.MTU)

	retry := time.Tick(c.cf.Retry)

	var rx *Frame
	var tx *Frame

	next := func() {
		if tx != nil || bw.Len() == 0 {
			return
		}

		n, err := bw.Read(fb)

		if err != nil && err != io.EOF {
			panic(err)
		}

		tx = &Frame{<-sn, 0, byte(n), fb[:n]}

		select {
		case c.fw <- tx:
		case <-retry:
		}
	}

	for {
		select {
		case c.cr <- br:
			<-c.cr

		case c.cw <- bw:
			<-c.cw
			next()

		case f := <-c.fr:
			// Ack frame.
			if f.id == 0 {
				if tx != nil && tx.id == f.ack {
					tx = nil
					next()
				}

				break
			}

			// Data frame. Is it new?
			if rx == nil || rx.id != f.id {
				rx = f
				br.Write(f.data)
			}

			// Send ack.
			c.fw <- &Frame{0, f.id, 0, nil}

		case <-retry:
			next()

			if tx == nil {
				break
			}

			select {
			case c.fw <- tx:
			default:
			}
		}
	}
}

func (c *Conn) Read(p []byte) (int, error) {
	for r := range c.cr {
		n, err := r.Read(p)

		c.cr <- r

		if n > 0 {
			return n, err
		}

		time.Sleep(c.cf.Retry * 2)
	}

	return 0, nil
}

func (c *Conn) Write(p []byte) (int, error) {
	w := <-c.cw

	n, err := w.Write(p)

	c.cw <- w

	return n, err
}

// 0         1       2        3         4...
// byte:0xEB byte:id byte:ack byte:size data... uint32:crc32
func (f *Frame) Decode(r io.Reader) error {
	b := make([]byte, 4)

	if _, err := r.Read(b); err != nil {
		return err
	}

	if b[0] != start {
		return ErrInvalidStart
	}

	f.id, f.ack, f.size = b[1], b[2], b[3]

	f.data = make([]byte, int(f.size))

	// Frame data
	if _, err := r.Read(f.data); err != nil {
		return err
	}

	// Frame CRC
	var c uint32

	if err := binary.Read(r, order, &c); err != nil {
		return err
	}

	b = append(b, f.data...)

	if c != crc32.ChecksumIEEE(b) {
		return ErrChecksum
	}

	return nil
}

func (f *Frame) Encode(w io.Writer) error {
	b, err := f.MarshalBinary()

	if err != nil {
		return err
	}

	if _, err := w.Write(b); err != nil {
		return err
	}

	return binary.Write(w, order, crc32.ChecksumIEEE(b))
}

func (f *Frame) MarshalBinary() ([]byte, error) {
	b := make([]byte, 4)

	b[0] = start
	b[1] = f.id
	b[2] = f.ack
	b[3] = f.size

	b = append(b, f.data...)

	return b, nil
}
