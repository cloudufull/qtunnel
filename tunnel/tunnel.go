package tunnel

import (
    "io"
    "net"
    "log"
    "time"
    "sync/atomic"
    "strconv"
    "encoding/binary"
    "github.com/juju/ratelimit"
    "github.com/pierrec/lz4/v4"
)

type Tunnel struct {
    faddr, baddr *net.TCPAddr
    clientMode int 
    cryptoMethod string
    secret []byte
    sessionsCount int32
    pool *recycler
    limit_buket *ratelimit.Bucket 
    wtmout int64
}

func NewTunnel(faddr, baddr string, clientMode int, cryptoMethod, secret string, size uint32,speed int64,tmwt int64) *Tunnel {
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
        wtmout: tmwt,
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
    } else {
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
    //1 client ,2 server ,3 switch_mode ,11 compress_client ,12 compress_server 
    switch t.clientMode {
           case 1,2:
                fconn = NewConn(conn, nil, t.pool,t.wtmout)
                bconn = NewConn(conn2, cipher, t.pool,t.wtmout)
                go t.pipe(bconn, fconn, writeChan)
                go t.pipe(fconn, bconn, readChan)
           case 3:
                fconn = NewConn(conn, cipher, t.pool,t.wtmout)
                bconn = NewConn(conn2, nil, t.pool,t.wtmout)
                go t.pipe(bconn, fconn, writeChan)
                go t.pipe(fconn, bconn, readChan)
           case 11:
                go compress(conn,conn2) 
                go uncompress(conn2,conn) 
           case 12:
                go compress(conn2,conn) 
                go uncompress(conn,conn2) 
    }
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

// read conn1 after compress write to conn2
func compress(conn1 ,conn2 net.Conn) {
    defer func(){
          conn1.Close()
          conn2.Close()
    }()
    mtu := 32768 
    packet := make([]byte, mtu)
    result := make([]byte, lz4.CompressBlockBound(int(1500))+2)
    bs := make([]byte, 2)
    alert_tms := 0
    for {
        n, err := conn1.Read(packet)
        if err != nil {
           if alert_tms < 1 {
               log.Printf("Disconnect %s\n", conn1.RemoteAddr().String())
               log.Printf("(%d) compress error  : %v\n", alert_tms,err) 
               alert_tms = 2 
           }
           continue
        }
        alert_tms = 0
        zn, _ := lz4.CompressBlock(packet[:n], result[2:], nil)
        binary.LittleEndian.PutUint16(bs, uint16(zn))
        copy(result, bs)
        conn2.Write(result[:zn+2])
    }
}

// read from conn1 then uncompress to conn2 
func uncompress(conn1 ,conn2 net.Conn) {
    defer func(){
          conn1.Close()
          conn2.Close()
    }()
    mtu := 32768 
    packet := make([]byte, lz4.CompressBlockBound(int(1500))+2)
    result := make([]byte, mtu)
    stream := make([]byte, 0)
    alert_tms := 0
    for {
        left, err := conn1.Read(packet)
        if err != nil {
            if alert_tms < 1 {
               log.Printf("Disconnect %s\n", conn1.RemoteAddr().String())
               log.Printf("(%d) uncompress error  : %v\n", alert_tms,err) 
               alert_tms = 2 
            }
            continue
        }
        alert_tms = 0
        stream = append(stream, packet[:left]...)

        for len(stream) > 2 {
            bs := binary.LittleEndian.Uint16(stream[:2])
            if len(stream) < int(bs)+2 {
                break
            }
            n, err := lz4.UncompressBlock(stream[2:bs+2], result)
            if err != nil {
                log.Print(err)
                stream = stream[2+bs:]
                break
            }
            stream = stream[2+bs:]
            conn2.Write(result[:n])
        }
    }

}

