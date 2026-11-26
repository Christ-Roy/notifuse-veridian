package safehttpclient

import (
	"net"
	"testing"
)

func TestIsPrivateIP_VeridianBloqueLIPv4CacheeEtLesReserves(t *testing.T) {
	for _, s := range []string{"::7f00:1", "64:ff9b::7f00:1", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1", "192.0.0.1", "198.18.0.1", "224.0.0.1", "240.0.0.1", "255.255.255.255", "fec0::1"} {
		if !IsPrivateIP(net.ParseIP(s)) {
			t.Errorf("%s laissé passer", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "93.184.216.34", "2001:4860:4860::8888", "2a00:1450:4007::64"} {
		if IsPrivateIP(net.ParseIP(s)) {
			t.Errorf("%s bloqué à tort (témoin public)", s)
		}
	}
}
