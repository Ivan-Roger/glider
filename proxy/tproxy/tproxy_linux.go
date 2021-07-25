package tproxy

import (
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/nadoo/glider/log"
	"github.com/nadoo/glider/pool"
	"github.com/nadoo/glider/proxy"
)

// TProxy struct.
type TProxy struct {
	proxy proxy.Proxy
	addr  string
}

func init() {
	proxy.RegisterServer("tproxy", NewTProxyServer)
}

// nat mapping
var nm sync.Map

// NewTProxy returns a tproxy.
func NewTProxy(s string, p proxy.Proxy) (*TProxy, error) {
	u, err := url.Parse(s)
	if err != nil {
		log.F("[tproxy] parse err: %s", err)
		return nil, err
	}

	tp := &TProxy{
		proxy: p,
		addr:  u.Host,
	}

	return tp, nil
}

// NewTProxyServer returns a udp tunnel server.
func NewTProxyServer(s string, p proxy.Proxy) (proxy.Server, error) {
	return NewTProxy(s, p)
}

// ListenAndServe listens on server's addr and serves connections.
func (s *TProxy) ListenAndServe() {
	// go s.ListenAndServeTCP()
	s.ListenAndServeUDP()
}

// ListenAndServeTCP listens and serves tcp.
func (s *TProxy) ListenAndServeTCP() {
	log.F("[tproxy] tcp mode not supported now, please use 'redir' instead")
}

// Serve serves tcp conn.
func (s *TProxy) Serve(c net.Conn) {
	log.F("[tproxy] func Serve: can not be called directly")
}

// ListenAndServeUDP listens and serves udp.
func (s *TProxy) ListenAndServeUDP() {
	laddr, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		log.F("[tproxyu] failed to resolve addr %s: %v", s.addr, err)
		return
	}

	lc, err := ListenUDP("udp", laddr)
	if err != nil {
		log.F("[tproxyu] failed to listen on %s: %v", s.addr, err)
		return
	}
	defer lc.Close()

	log.F("[tproxyu] listening UDP on %s", s.addr)

	for {
		buf := pool.GetBuffer(proxy.UDPBufSize)
		n, srcAddr, dstAddr, err := ReadFromUDP(lc, buf)
		if err != nil {
			log.F("[tproxyu] read error: %v", err)
			continue
		}

		var session *Session
		sessionKey := srcAddr.String()

		v, ok := nm.Load(sessionKey)
		if !ok && v == nil {
			session = &Session{sessionKey, srcAddr, dstAddr, make(chan []byte, 32)}
			nm.Store(sessionKey, session)
			go s.ServeSession(session)
		} else {
			session = v.(*Session)
		}
		session.msgQueue <- buf[:n]
	}
}

// ServeSession serves a udp session.
func (s *TProxy) ServeSession(session *Session) {
	dstPC, dialer, writeTo, err := s.proxy.DialUDP("udp", session.dst.String())
	if err != nil {
		log.F("[tproxyu] dial to %s error: %v", session.dst, err)
		return
	}
	defer dstPC.Close()

	srcPC, err := ListenPacket(session.dst)
	if err != nil {
		log.F("[tproxyu] ListenPacket as %s error: %v", session.dst, err)
		return
	}
	defer srcPC.Close()

	log.F("[tproxyu] %s <-> %s via %s", session.src, session.dst, dialer.Addr())

	go func() {
		proxy.RelayUDP(srcPC, session.src, dstPC, 2*time.Minute)
		nm.Delete(session.key)
		close(session.msgQueue)
	}()

	for data := range session.msgQueue {
		_, err = dstPC.WriteTo(data, writeTo)
		if err != nil {
			log.F("[tproxyu] writeTo %s error: %v", writeTo, err)
		}
		pool.PutBuffer(data)
	}
}

// Session is a udp session
type Session struct {
	key      string
	src, dst *net.UDPAddr
	msgQueue chan []byte
}