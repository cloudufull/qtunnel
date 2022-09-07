package tunnel

import (
    "io"
    "net"
    "log"
    "time"
    "sync/atomic"
    "strconv"
    "github.com/juju/ratelimit"
)

type Tunnel struct {
    faddr, baddr *net.TCPAddr
    clientMode int 
    cryptoMethod string
    secret []byte
    sessionsCount int32
    pool *recycler
    limit_buket *ratelimit.Bucket 
}

func NewTunnel(faddr, baddr string, clientMode int, cryptoMethod, secret string, size uint32,speed int64) *Tunnel {
    a1, err := net.ResolveTCPAddr("tcp", faddr)
    if err != nil {
        log.Fatalln("resolve frontend error:", err)
    }
    a2, err := net.ResolveTCPAddr("tcp", baddr)
    if err != nil {
        log.Fatalln("resolve backend error:", err)
    }
    var rate float64;var bucket *ratelimit.Bucket;
    if (speed>0){
         rate,_= strconv.ParseFloat(strconv.FormatInt(speed,10), 64) 
         bucket = ratelimit.NewBucketWithRate(100*1024*10*rate, 100*1024*10*speed)
    }
    return &Tunnel{
        faddr: a1,
        baddr: a2,
        clientMode: clientMode,
        cryptoMethod: cryptoMethod,
        secret: []byte(secret),
        sessionsCount: 0,
        pool: NewRecycler(size),
        limit_buket: bucket,
    }
}

func (t *Tunnel) pipe(dst, src *Conn, c chan int64) {
    defer func() {
        dst.CloseWrite()
        src.CloseRead()
    }()
    var n int64;var err error
    if t.limit_buket!=nil{
       n, err = io.Copy(dst, ratelimit.Reader(src,t.limit_buket))
    }else{
       n, err = io.Copy(dst, src)
    }
    if err != nil {
        log.Printf("io.Copy: %v\n", err)
    }
    c <- n
}

func (t *Tunnel) transport(conn net.Conn) {
    start := time.Now()
    conn2, err := net.DialTCP("tcp", nil, t.baddr)
    if err != nil {
        log.Print(err)
        return
    }
    connectTime := time.Now().Sub(start)
    start = time.Now()
    cipher := NewCipher(t.cryptoMethod, t.secret)
    readChan := make(chan int64)
    writeChan := make(chan int64)
    var readBytes, writeBytes int64
    atomic.AddInt32(&t.sessionsCount, 1)
    var bconn, fconn *Conn
    //1 client 2 server 3 switch_mode
    switch t.clientMode {
           case 1:
                fconn = NewConn(conn, nil, t.pool)
                bconn = NewConn(conn2, cipher, t.pool)
           case 2:
                fconn = NewConn(conn, cipher, t.pool)
                bconn = NewConn(conn2, nil, t.pool)
           case 3:
                fconn = NewConn(conn, nil, t.pool)
                bconn = NewConn(conn2, nil, t.pool)
    }
    go t.pipe(bconn, fconn, writeChan)
    go t.pipe(fconn, bconn, readChan)
    readBytes = <-readChan
    writeBytes = <-writeChan
    transferTime := time.Now().Sub(start)
    log.Printf("r:%d w:%d ct:%.3f t:%.3f [#%d]", readBytes, writeBytes,
        connectTime.Seconds(), transferTime.Seconds(), t.sessionsCount)
    atomic.AddInt32(&t.sessionsCount, -1)
}

func (t *Tunnel) Start() {
    ln, err := net.ListenTCP("tcp", t.faddr)
    if err != nil {
        log.Fatal(err)
    }
    defer ln.Close()

    for {
        conn, err := ln.AcceptTCP()
        if err != nil {
            log.Println("accept:", err)
            continue
        }
        go t.transport(conn)
    }
}
