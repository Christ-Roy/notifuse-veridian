package domauth

import (
	"context"
	"net"
	"sync"
)

// fakeDNS : résolveur simulé. txt[nom] = enregistrements ; fail[nom] = panne (temperror) ; absent = NXDOMAIN.
type fakeDNS struct {
	mu    sync.Mutex
	txt   map[string][]string
	fail  map[string]bool
	asked []string
}

func newDNS() *fakeDNS { return &fakeDNS{txt: map[string][]string{}, fail: map[string]bool{}} }

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, name)
	if f.fail[name] {
		return nil, &net.DNSError{Err: "server misbehaving", Name: name, IsTemporary: true}
	}
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
func (f *fakeDNS) LookupMX(context.Context, string) ([]*net.MX, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}
func (f *fakeDNS) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}
func (f *fakeDNS) LookupAddr(context.Context, string) ([]string, error) {
	return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
}
