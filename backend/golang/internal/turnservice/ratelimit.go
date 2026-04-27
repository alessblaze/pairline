package turnservice

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

func newRateLimiter(kbps int) *rate.Limiter {
	// kbps is kilobits per second.
	// 1 kilobit = 1000 bits.
	// bytes per second = (kbps * 1000) / 8
	bytesPerSec := float64(kbps) * 1000.0 / 8.0
	// burst size: 100ms worth of data.
	// Note: at low configured rates, the 65536 floor dominates. This provides a
	// generous minimum token bucket floor to avoid rejecting large packet-sized writes,
	// meaning it allows a burst larger than a true 100ms window at very low bitrates.
	burst := int(bytesPerSec / 10)
	if burst < 65536 {
		burst = 65536
	}
	return rate.NewLimiter(rate.Limit(bytesPerSec), burst)
}

// waitNChunked calls WaitN in burst-sized chunks so that n > burst does not
// cause WaitN to return an immediate error (its documented behavior).
func waitNChunked(ctx context.Context, limiter *rate.Limiter, n int) error {
	burst := limiter.Burst()
	for n > 0 {
		chunk := n
		if chunk > burst {
			chunk = burst
		}
		if err := limiter.WaitN(ctx, chunk); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

// rateLimitedPacketConn enforces bandwidth limits on UDP (packet) connections.
// Policy: Packets exceeding the rate limit are immediately dropped without blocking.
// This is optimal for WebRTC audio/video as it triggers congestion control natively.
type rateLimitedPacketConn struct {
	net.PacketConn
	readLimiter  *rate.Limiter
	writeLimiter *rate.Limiter
}

func newRateLimitedPacketConn(conn net.PacketConn, kbps int) net.PacketConn {
	// Create separate limiters for read and write paths.
	// This enforces the kbps limit per-direction (yielding up to 2*kbps total bidirectional bandwidth).
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
		// Note: This silently swallows the drop.
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, addr)
}

// rateLimitedConn enforces bandwidth limits on TCP (stream) connections.
// Policy: Reads and writes exceeding the rate limit block to exert backpressure,
// respecting any connection deadlines that have been set.
type rateLimitedConn struct {
	net.Conn
	readLimiter   *rate.Limiter
	writeLimiter  *rate.Limiter
	readDeadline  atomic.Pointer[time.Time]
	writeDeadline atomic.Pointer[time.Time]
	closeCtx      context.Context
	closeCancel   context.CancelFunc
}

func newRateLimitedConn(conn net.Conn, kbps int) net.Conn {
	ctx, cancel := context.WithCancel(context.Background())
	// Create separate limiters for read and write paths.
	// This enforces the kbps limit per-direction (yielding up to 2*kbps total bidirectional bandwidth).
	return &rateLimitedConn{
		Conn:         conn,
		readLimiter:  newRateLimiter(kbps),
		writeLimiter: newRateLimiter(kbps),
		closeCtx:     ctx,
		closeCancel:  cancel,
	}
}

func (c *rateLimitedConn) Close() error {
	c.closeCancel()
	return c.Conn.Close()
}

func (c *rateLimitedConn) SetDeadline(t time.Time) error {
	c.readDeadline.Store(&t)
	c.writeDeadline.Store(&t)
	return c.Conn.SetDeadline(t)
}

func (c *rateLimitedConn) SetReadDeadline(t time.Time) error {
	c.readDeadline.Store(&t)
	return c.Conn.SetReadDeadline(t)
}

func (c *rateLimitedConn) SetWriteDeadline(t time.Time) error {
	c.writeDeadline.Store(&t)
	return c.Conn.SetWriteDeadline(t)
}

func (c *rateLimitedConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		ctx := c.closeCtx
		if ptr := c.readDeadline.Load(); ptr != nil && !ptr.IsZero() {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(ctx, *ptr)
			defer cancel()
		}
		if waitErr := waitNChunked(ctx, c.readLimiter, n); waitErr != nil {
			// If the limiter times out, we still return the data we successfully read (n > 0).
			// This matches Go's partial success semantics, though callers setting tight deadlines
			// must tolerate seeing a timeout error that originated from the limiter wait 
			// rather than the underlying socket I/O.
			return n, waitErr
		}
	}
	return
}

func (c *rateLimitedConn) Write(b []byte) (n int, err error) {
	burst := c.writeLimiter.Burst()
	
	// We chunk both the limiter wait and the actual network Write here instead of using
	// waitNChunked. This guarantees we only debit tokens for bytes we actually attempt to
	// write to the wire, avoiding severe token over-charging if a partial write or error occurs.
	for len(b) > 0 {
		chunkSize := len(b)
		if chunkSize > burst {
			chunkSize = burst
		}
		
		ctx := c.closeCtx
		var cancel context.CancelFunc
		if ptr := c.writeDeadline.Load(); ptr != nil && !ptr.IsZero() {
			ctx, cancel = context.WithDeadline(ctx, *ptr)
		}
		
		waitErr := c.writeLimiter.WaitN(ctx, chunkSize)
		if cancel != nil {
			cancel()
		}
		if waitErr != nil {
			return n, waitErr
		}

		written, writeErr := c.Conn.Write(b[:chunkSize])
		n += written
		
		if writeErr != nil {
			return n, writeErr
		}
		if written == 0 {
			return n, io.ErrShortWrite
		}
		
		b = b[written:]
	}
	return n, nil
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

