package main

import (
	rand "crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var wellKnownPath = "/.well-known/acme-challenge/"

type challSrv struct {
	hoMu        sync.RWMutex
	httpOne     map[string]string
	httpOneAddr string

	tlsOneAddr string
}

func newChallSrv(httpOneAddr, tlsOneAddr string) *challSrv {
	return &challSrv{
		httpOne:     make(map[string]string),
		httpOneAddr: httpOneAddr,
		tlsOneAddr:  tlsOneAddr,
	}
}

// Run runs the challenge server on the configured address
func (s *challSrv) run() {
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		err := s.httpOneServer(wg)
		if err != nil {
			fmt.Printf("[!] http-0 server failed: %s\n", err)
			os.Exit(1)
		}
	}()
	wg.Add(1)
	go func() {
		err := s.tlsOneServer(wg)
		if err != nil {
			fmt.Printf("[!] tls-sni-01 server failed: %s\n", err)
			os.Exit(1)
		}
	}()
	wg.Wait()
}

func (s *challSrv) addHTTPOneChallenge(token, content string) {
	s.hoMu.Lock()
	defer s.hoMu.Unlock()
	s.httpOne[token] = content
}

func (s *challSrv) deleteHTTPOneChallenge(token string) {
	s.hoMu.Lock()
	defer s.hoMu.Unlock()
	if _, ok := s.httpOne[token]; ok {
		delete(s.httpOne, token)
	}
}

func (s *challSrv) getHTTPOneChallenge(token string) (string, bool) {
	s.hoMu.RLock()
	defer s.hoMu.RUnlock()
	content, present := s.httpOne[token]
	return content, present
}

func (s *challSrv) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestPath := r.URL.Path
	if strings.HasPrefix(requestPath, wellKnownPath) {
		token := requestPath[len(wellKnownPath):]
		if auth, found := s.getHTTPOneChallenge(token); found {
			fmt.Fprintf(w, "%s", auth)
			s.deleteHTTPOneChallenge(token)
		}
	}
}

func (s *challSrv) httpOneServer(wg *sync.WaitGroup) error {
	fmt.Println("[+] Starting http-01 server")
	srv := &http.Server{
		Addr:         s.httpOneAddr,
		Handler:      s,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}
	srv.SetKeepAlivesEnabled(false)
	wg.Done()
	return srv.ListenAndServe()
}

func (s *challSrv) tlsOneServer(wg *sync.WaitGroup) error {
	fmt.Println("[+] Starting tls-sni-01 server")

	tinyKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	l, err := tls.Listen("tcp", s.tlsOneAddr, &tls.Config{
		GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			t := &x509.Certificate{
				SerialNumber: big.NewInt(1),
				DNSNames:     []string{clientHello.ServerName},
				Subject:      pkix.Name{CommonName: "test"},
			}
			inner, err := x509.CreateCertificate(rand.Reader, t, t, tinyKey.Public(), tinyKey)
			if err != nil {
				fmt.Printf("[!] Failed to sign test certificate: %s\n", err)
				return nil, nil
			}
			return &tls.Certificate{Certificate: [][]byte{inner}, PrivateKey: tinyKey}, nil
		},
	})
	if err != nil {
		return err
	}
	wg.Done()
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("[!] TLS connection failed: %s\n", err)
			continue
		}
		go func() {
			defer conn.Close()
			tlsConn := conn.(*tls.Conn)
			err = tlsConn.Handshake()
			if err != nil {
				fmt.Printf("[!] TLS handshake failed: %s\n", err)
				return
			}
		}()
	}
}
