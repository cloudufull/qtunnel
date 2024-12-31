package main

import (
	"flag"
	"qtunnel/goconfig"
	"qtunnel/godaemon"
	"log"
	"log/syslog"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"qtunnel/tunnel"
	"time"
	"net"
    "strings"
)

func isTagInSection(sections []string, tag string) bool {
	if tag == "" {
		return false
	}

	for _, v := range sections {
		if v == tag {
			return true
		}
	}

	return false
}

func waitSignal() {
	var sigChan = make(chan os.Signal, 1)
	signal.Notify(sigChan,syscall.SIGINT,syscall.SIGTERM )
	for sig := range sigChan {
		if sig == syscall.SIGINT || sig == syscall.SIGTERM {
			log.Printf("terminated by signal %v\n", sig)
			return
		}
	}
}

func check_port(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func main() {
	var faddr, baddr, cryptoMethod, secret, logTo, conf, tag string
    var speed int64
    var wtmout int64
	var clientMode, daemon,switchMode bool
	var buffer uint
    var trans_mode int =1 // 1 client 2 server 3 switch_mode
	flag.StringVar(&logTo, "logto", "stdout", "stdout or syslog")
	flag.StringVar(&faddr, "listen", ":9001", "host:port qtunnel listen on")
	flag.StringVar(&baddr, "backend", "127.0.0.1:6400", "host:port of the backend")
	flag.StringVar(&cryptoMethod, "crypto", "rc4", "encryption method")
	flag.StringVar(&secret, "secret", "", "password used to encrypt the data")
	flag.StringVar(&tag, "tag", "", "only setup the tag in config file")
	flag.UintVar(&buffer, "buffer", 4096, "tunnel buffer size")
	flag.BoolVar(&clientMode, "clientmode", false, "if running at client mode")
	flag.BoolVar(&switchMode, "switchmode", false, "wether runing at switchMode,redirect port without secret")
	flag.BoolVar(&daemon, "daemon", false, "running in daemon mode")
    flag.Int64Var(&speed,"speed", 0, "transmission speed rate MBps")
    flag.Int64Var(&wtmout,"timeout", 30, "close connection after it stay idle $timeout  minutes  ")
	flag.StringVar(&conf, "conf", "", `read connection setup from config file:
    eg:
       [compress] 
       faddr = 10.3.246.149:1316  # from local
       baddr = 10.3.246.150:1316  # trans to remote
       cryptoMethod = rc4         # only support rc4 .... 
       secret = 1xtneltgt1gdraa.6 # encryption key  
       clientmode = true   # when in server side  clientmode=false
       switchmode=true     # when switch=true ,trans data no need secret and cryptoMethod encrypt any more ！
       compress=1          # when switchmode=true  clientmode is set and  compress=true ,use lz4 compress net flow
       timeout=180         # conn be idle 180min disconnect
       speed=5             # transmission speed rate MBps, here example meas limit 5MBps 
     `)
	flag.Parse()

	log.SetOutput(os.Stdout)
	if logTo == "syslog" {
		w, err := syslog.New(syslog.LOG_INFO, "qtunnel")
		if err != nil {
			log.Fatal(err)
		}
		log.SetOutput(w)
	}
	CurDir, _ := os.Getwd()

	if daemon == true {
		godaemon.MakeDaemon(&godaemon.DaemonAttr{})
	}
	// start from config file for multi-front-port
	if len(conf) > 0 {
		if match, _ := regexp.MatchString("^[^/]", conf); match {
			conf = CurDir + "/" + conf
		}
		c, err := goconfig.ReadConfigFile(conf)
		if err != nil {
			log.Println("read error from %s file", conf)
			os.Exit(1)
		}
		sections := c.GetSections()
		for _, s := range sections {
			if s == "default" {
				continue
			}
            var secrt string
            fdr, err := c.GetString(s, "faddr")
            bdr, err := c.GetString(s, "baddr")
            cmd, err := c.GetBool(s, "clientmode")
            smd, err := c.GetBool(s, "switchmode")
            compr, err := c.GetBool(s, "compress")
            cmth, err := c.GetString(s, "cryptoMethod")
            speed, err := c.GetInt64(s, "speed")
            if (err!=nil){speed=0}

            if smd  {
               trans_mode=3
               cmth="rc4"
               secrt="secret"
               
               if compr && cmd {
                  trans_mode=11
               }

               if compr && !cmd {
                  trans_mode=12
               }

            }else{
                 secrt, err = c.GetString(s, "secret")
                 if (err != nil) {
                    log.Fatalln("qtunnel config error with tag: %s, : -> can't run without secret under secret tunnel mode !!", tag)
                    os.Exit(1)
                }
                switch cmd {
                       case true:
                           trans_mode=1
                       case false:
                           trans_mode=2
                }
            }

            
            if (len(tag) > 0) {
                if (s==strings.ToLower(tag)){
		           if !isTagInSection(sections, strings.ToLower(tag)) {
		           	log.Printf("can not find tag %s, exit!", tag)
		           	os.Exit(1)
		           }
		    	   if check_port(fdr) {
		    	   	log.Printf("qtunnel already bind %s", fdr)
		    	   		os.Exit(1)
		    	   		continue
		    	   }
                   wtmout, err := c.GetInt64(s, "timeout")
                   if (err!=nil){
                       wtmout=30
                       log.Printf(`wtmout args need int type ,switch to 30min %s`,err)
                   }
                    log.Printf(`set timeout to %d s`,wtmout)
			        go func() {
			        	t := tunnel.NewTunnel(fdr, bdr, trans_mode, cmth, secrt, uint32(buffer),speed,wtmout)
			        	log.Printf("qtunnel start from %s to %s. use tag %s", fdr, bdr,tag)
			        	t.Start()
			        }()
                    break
                }
            }else{
		    	if check_port(fdr) {
		    		log.Printf("qtunnel already bind %s", fdr)
		    			os.Exit(1)
		    			continue
		    	}

                wtmout, err := c.GetInt64(s, "timeout")
                if (err!=nil){
                    wtmout=30
                    log.Printf(`wtmout args need int type ,switch to 30min %s`,err)
                }

                log.Printf(`set timeout to %d s`,wtmout)
			    go func() {
			       t := tunnel.NewTunnel(fdr, bdr, trans_mode, cmth, secrt, uint32(buffer),speed,wtmout)
			       log.Printf("qtunnel all tag ,now start %s from %s to %s.",s,fdr,bdr)
			    	t.Start()
			    }()
            }
		}
	} else {
        if switchMode{
           trans_mode=3
           cryptoMethod="rc4"
           secret="secret"
        }else{
           if (secret==""){
		      log.Fatalln("qtunnel config error with tag: %s, : -> can't run without secret under secret tunnel mode !!", tag)
		      os.Exit(1)
           }
           switch clientMode {
                  case true:
                      trans_mode=1 
                  case false:
                      trans_mode=2 
           }
        } 
        log.Printf(`set timeout to %d s`,wtmout)
		t := tunnel.NewTunnel(faddr, baddr, trans_mode, cryptoMethod, secret, uint32(buffer),speed,wtmout)
		log.Println("qtunnel started.")
		go t.Start()
	}
	waitSignal()
}
