package tunnel

import (
    "net"
    "time"
)

type Conn struct {
    conn net.Conn
    cipher *Cipher
    pool *recycler
    wtmout int64
}

func NewConn(conn net.Conn, cipher *Cipher, pool *recycler,tmwt int64) *Conn {
    return &Conn{
        conn: conn,
        cipher: cipher,
        pool: pool,
        wtmout: tmwt,
    }
}

func (c *Conn) Read(b []byte) (int, error) {
    c.conn.SetReadDeadline(time.Now().Add(time.Duration(c.wtmout) * time.Minute))
    if c.cipher == nil {
       n, err := c.conn.Read(b)
       return n, err
    }
    n, err := c.conn.Read(b)
    if n > 0 {
        c.cipher.decrypt(b[0:n], b[0:n])
    }
    return n, err
}

func (c *Conn) Write(b []byte) (int, error) {
    if c.cipher == nil {
       n,err := c.conn.Write(b)
       return n,err 
    }
    c.cipher.encrypt(b, b)
    return c.conn.Write(b)
}

func (c *Conn) Close() {
    c.conn.Close()
}

func (c *Conn) CloseRead() {
    if conn, ok := c.conn.(*net.TCPConn); ok {
        conn.CloseRead()
    }
}

func (c *Conn) CloseWrite() {
    if conn, ok := c.conn.(*net.TCPConn); ok {
        conn.CloseWrite()
    }
}
