package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

var reservedNetworks = []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "::/96", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64", "2001::/23", "2002::/16", "2001:db8::/32", "3fff::/20", "fc00::/7", "fe80::/10", "ff00::/8"}

func publicIP(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() {
		return false
	}
	for _, p := range reservedNetworks {
		if netip.MustParsePrefix(p).Contains(a) {
			return false
		}
	}
	return true
}
func safeDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("域名解析失败")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("禁止访问非公网地址")
		}
	}
	// Dial the validated IP itself. Never let a second DNS lookup rebind it.
	d := net.Dialer{Timeout: 15 * time.Second}
	for _, ip := range ips {
		var conn net.Conn
		conn, err = d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, err
}
func safeHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: safeDial, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, DisableKeepAlives: true}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("重定向过多")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("不安全的重定向")
		}
		return nil
	}}
}

// Only published on the private Docker network, never a host port. The
// converter has no external network and must use this authenticated proxy.
func (s *server) egressProxy(w http.ResponseWriter, r *http.Request) {
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("coralbay:"+s.actionToken))
	if s.actionToken == "" || !secureEqual(r.Header.Get("Proxy-Authorization"), want) {
		w.WriteHeader(407)
		return
	}
	if r.Method == http.MethodConnect {
		upstream, err := safeDial(r.Context(), "tcp", r.Host)
		if err != nil {
			http.Error(w, "destination denied", 403)
			return
		}
		defer upstream.Close()
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "unsupported", 500)
			return
		}
		downstream, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer downstream.Close()
		deadline := time.Now().Add(2 * time.Minute)
		upstream.SetDeadline(deadline)
		downstream.SetDeadline(deadline)
		rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		rw.Flush()
		done := make(chan struct{})
		go func() { io.Copy(upstream, rw); upstream.Close(); close(done) }()
		io.Copy(downstream, upstream)
		downstream.Close()
		<-done
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "method denied", 405)
		return
	}
	if r.URL.Scheme != "http" || r.URL.User != nil {
		http.Error(w, "invalid URL", 400)
		return
	}
	req := r.Clone(r.Context())
	req.RequestURI = ""
	req.Header = r.Header.Clone()
	for _, h := range []string{"Proxy-Authorization", "Proxy-Connection", "Connection", "Upgrade", "TE", "Trailer", "Transfer-Encoding"} {
		req.Header.Del(h)
	}
	resp, err := safeHTTPClient(2 * time.Minute).Do(req)
	if err != nil {
		http.Error(w, "upstream denied or unavailable", 502)
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if strings.EqualFold(k, "Connection") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, io.LimitReader(resp.Body, 32<<20))
}
