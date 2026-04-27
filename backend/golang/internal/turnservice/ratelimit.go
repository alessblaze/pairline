package turnservice

import (
	"context"
	"net"
	"time"

	"golang.org/x/time/rate"
)

func newRateLimiter(kbps int) *rate.Limiter {
	// kbps is kilobits per second.
	// 1 kilobit = 1000 bits.
	// bytes per second = (kbps * 1000) / 8
	bytesPerSec := float64(kbps) * 1000.0 / 8.0
	// burst size: 1 second worth of data, or at least 65536 to accommodate max UDP packet size
	burst := int(bytesPerSec)
	if burst < 65536 {
		burst = 65536
	}
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

type rateLimitedPacketConn struct {
	net.PacketConn
	readLimiter  *rate.Limiter
	writeLimiter *rate.Limiter
}

func newRateLimitedPacketConn(conn net.PacketConn, kbps int) net.PacketConn {
	return &rateLimitedPacketConn{
		PacketConn:   conn,
		readLimiter:  newRateLimiter(kbps),
		writeLimiter: newRateLimiter(kbps),
	}
}

func (c *rateLimitedPacketConn) ReadFrom(p []byte) (n int, addr net.Addr, err error) {
	for {
		n, addr, err = c.PacketConn.ReadFrom(p)
		if err != nil {
			return
		}
		if n > 0 && !c.readLimiter.AllowN(time.Now(), n) {
			// Drop the packet and read the next one to limit bandwidth
			// without blocking the goroutine reading from this socket.
			continue
		}
		return
	}
}

func (c *rateLimitedPacketConn) WriteTo(p []byte, addr net.Addr) (n int, err error) {
	if !c.writeLimiter.AllowN(time.Now(), len(p)) {
		// Drop the packet to limit bandwidth without blocking.
		// Return success so the caller doesn't treat this as a fatal network error.
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, addr)
}

type rateLimitedConn struct {
	net.Conn
	readLimiter  *rate.Limiter
	writeLimiter *rate.Limiter
}

func newRateLimitedConn(conn net.Conn, kbps int) net.Conn {
	return &rateLimitedConn{
		Conn:         conn,
		readLimiter:  newRateLimiter(kbps),
		writeLimiter: newRateLimiter(kbps),
	}
}

func (c *rateLimitedConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		_ = c.readLimiter.WaitN(context.Background(), n)
	}
	return
}

func (c *rateLimitedConn) Write(b []byte) (n int, err error) {
	_ = c.writeLimiter.WaitN(context.Background(), len(b))
	return c.Conn.Write(b)
}

type rateLimitedListener struct {
	net.Listener
	limitKbps int
}

func newRateLimitedListener(l net.Listener, kbps int) net.Listener {
	return &rateLimitedListener{Listener: l, limitKbps: kbps}
}

func (l *rateLimitedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return newRateLimitedConn(c, l.limitKbps), nil
}

