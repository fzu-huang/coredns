package kubernetes

import (
	"net"
	"strings"
	"testing"

	"github.com/coredns/coredns/plugin/transfer"

	"github.com/miekg/dns"
)

func TestKubernetesTransferNonAuthZone(t *testing.T) {
	k := New([]string{"cluster.local."})
	k.APIConn = &APIConnServeTest{}
	k.Namespaces = map[string]struct{}{"testns": {}, "kube-system": {}}
	k.localIPs = []net.IP{net.ParseIP("10.0.0.10")}

	dnsmsg := &dns.Msg{}
	dnsmsg.SetAxfr("example.com")

	_, err := k.Transfer("example.com", 0)
	if err != transfer.ErrNotAuthoritative {
		t.Error(err)
	}
}

func TestKubernetesAXFR(t *testing.T) {
	k := New([]string{"cluster.local."})
	k.APIConn = &APIConnServeTest{}
	k.Namespaces = map[string]struct{}{"testns": {}, "kube-system": {}}
	k.localIPs = []net.IP{net.ParseIP("10.0.0.10")}

	dnsmsg := &dns.Msg{}
	dnsmsg.SetAxfr(k.Zones[0])

	ch, err := k.Transfer(k.Zones[0], 0)
	if err != nil {
		t.Error(err)
	}
	validateAXFR(t, ch, false)
}

func TestKubernetesMultiClusterAXFR(t *testing.T) {
	k := New([]string{"cluster.local.", "clusterset.local."})
	k.opts.multiclusterZones = []string{"clusterset.local."}
	k.APIConn = &APIConnServeTest{}
	k.Namespaces = map[string]struct{}{"testns": {}, "kube-system": {}}
	k.localIPs = []net.IP{net.ParseIP("10.0.0.10")}

	dnsmsg := &dns.Msg{}
	dnsmsg.SetAxfr("clusterset.local.")

	ch, err := k.Transfer("clusterset.local.", 0)
	if err != nil {
		t.Error(err)
	}
	validateAXFR(t, ch, true)
}

func TestKubernetesIXFRFallback(t *testing.T) {
	k := New([]string{"cluster.local."})
	k.APIConn = &APIConnServeTest{}
	k.Namespaces = map[string]struct{}{"testns": {}, "kube-system": {}}
	k.localIPs = []net.IP{net.ParseIP("10.0.0.10")}

	dnsmsg := &dns.Msg{}
	dnsmsg.SetAxfr(k.Zones[0])

	ch, err := k.Transfer(k.Zones[0], 1)
	if err != nil {
		t.Error(err)
	}
	validateAXFR(t, ch, false)
}

func TestKubernetesIXFRCurrent(t *testing.T) {
	k := New([]string{"cluster.local."})
	k.APIConn = &APIConnServeTest{}
	k.Namespaces = map[string]struct{}{"testns": {}, "kube-system": {}}
	k.localIPs = []net.IP{net.ParseIP("10.0.0.10")}

	dnsmsg := &dns.Msg{}
	dnsmsg.SetAxfr(k.Zones[0])

	ch, err := k.Transfer(k.Zones[0], 3)
	if err != nil {
		t.Error(err)
	}

	var gotRRs []dns.RR
	for rrs := range ch {
		gotRRs = append(gotRRs, rrs...)
	}

	// ensure only one record is returned
	if len(gotRRs) > 1 {
		t.Errorf("Expected only one answer, got %d", len(gotRRs))
	}

	// Ensure first record is a SOA
	if gotRRs[0].Header().Rrtype != dns.TypeSOA {
		t.Error("Invalid transfer response, does not start with SOA record")
	}
}

func validateAXFR(t *testing.T, ch <-chan []dns.RR, multicluster bool) {
	t.Helper()
	xfr := []dns.RR{}
	for rrs := range ch {
		xfr = append(xfr, rrs...)
	}
	if xfr[0].Header().Rrtype != dns.TypeSOA {
		t.Error("Invalid transfer response, does not start with SOA record")
	}

	expected := expectedZone
	if multicluster {
		expected = expectedMultiClusterZone
	}
	zp := dns.NewZoneParser(strings.NewReader(expected), "", "")
	i := 0
	for rr, ok := zp.Next(); ok; rr, ok = zp.Next() {
		if !dns.IsDuplicate(rr, xfr[i]) {
			t.Fatalf("Record %d, expected\n%v\n, got\n%v", i, rr, xfr[i])
		}
		i++
	}

	if err := zp.Err(); err != nil {
		t.Fatal(err)
	}
}

const expectedZone = `
cluster.local.	5	IN	SOA	ns.dns.cluster.local. hostmaster.cluster.local. 3 7200 1800 86400 5
cluster.local.	5	IN	NS	ns.dns.cluster.local.
ns.dns.cluster.local.	5	IN	A	10.0.0.10
external.testns.svc.cluster.local.	5	IN	CNAME	ext.interwebs.test.
external-to-service.testns.svc.cluster.local.	5	IN	CNAME	svc1.testns.svc.cluster.local.
hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.2
172-0-0-2.hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.2
_http._tcp.hdls1.testns.svc.cluster.local.	5	IN	SRV	0 16 80 172-0-0-2.hdls1.testns.svc.cluster.local.
hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.3
172-0-0-3.hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.3
_http._tcp.hdls1.testns.svc.cluster.local.	5	IN	SRV	0 16 80 172-0-0-3.hdls1.testns.svc.cluster.local.
hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.4
dup-name.hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.4
_http._tcp.hdls1.testns.svc.cluster.local.	5	IN	SRV	0 16 80 dup-name.hdls1.testns.svc.cluster.local.
hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.5
dup-name.hdls1.testns.svc.cluster.local.	5	IN	A	172.0.0.5
_http._tcp.hdls1.testns.svc.cluster.local.	5	IN	SRV	0 16 80 dup-name.hdls1.testns.svc.cluster.local.
hdls1.testns.svc.cluster.local.	5	IN	AAAA	5678:abcd::1
5678-abcd--1.hdls1.testns.svc.cluster.local.	5	IN	AAAA	5678:abcd::1
_http._tcp.hdls1.testns.svc.cluster.local.	5	IN	SRV	0 16 80 5678-abcd--1.hdls1.testns.svc.cluster.local.
hdls1.testns.svc.cluster.local.	5	IN	AAAA	5678:abcd::2
5678-abcd--2.hdls1.testns.svc.cluster.local.	5	IN	AAAA	5678:abcd::2
_http._tcp.hdls1.testns.svc.cluster.local.	5	IN	SRV	0 16 80 5678-abcd--2.hdls1.testns.svc.cluster.local.
hdlsprtls.testns.svc.cluster.local.	5	IN	A	172.0.0.20
172-0-0-20.hdlsprtls.testns.svc.cluster.local.	5	IN	A	172.0.0.20
kubedns.kube-system.svc.cluster.local.	5	IN	A	10.0.0.10
kubedns.kube-system.svc.cluster.local.	5	IN	SRV	0 100 53 kubedns.kube-system.svc.cluster.local.
_dns._udp.kubedns.kube-system.svc.cluster.local.	5	IN	SRV	0 100 53 kubedns.kube-system.svc.cluster.local.
svc-dual-stack.testns.svc.cluster.local.	5	IN	A	10.0.0.3
svc-dual-stack.testns.svc.cluster.local.	5	IN	AAAA	10::3
svc-dual-stack.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svc-dual-stack.testns.svc.cluster.local.
_http._tcp.svc-dual-stack.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svc-dual-stack.testns.svc.cluster.local.
svc1.testns.svc.cluster.local.	5	IN	A	10.0.0.1
svc1.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svc1.testns.svc.cluster.local.
_http._tcp.svc1.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svc1.testns.svc.cluster.local.
svc6.testns.svc.cluster.local.	5	IN	AAAA	1234:abcd::1
svc6.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svc6.testns.svc.cluster.local.
_http._tcp.svc6.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svc6.testns.svc.cluster.local.
svcempty.testns.svc.cluster.local.	5	IN	A	10.0.0.1
svcempty.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svcempty.testns.svc.cluster.local.
_http._tcp.svcempty.testns.svc.cluster.local.	5	IN	SRV	0 100 80 svcempty.testns.svc.cluster.local.
cluster.local.	5	IN	SOA	ns.dns.cluster.local. hostmaster.cluster.local. 3 7200 1800 86400 5
`

const expectedMultiClusterZone = `
clusterset.local.	5	IN	SOA	ns.dns.clusterset.local. hostmaster.clusterset.local. 3 7200 1800 86400 5
clusterset.local.	5	IN	NS	ns.dns.clusterset.local.
ns.dns.clusterset.local.	5	IN	A	10.0.0.10
hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.2
172-0-0-2.hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.2
_http._tcp.hdls1.testns.svc.clusterset.local.	5	IN	SRV	0 16 80 172-0-0-2.hdls1.testns.svc.clusterset.local.
hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.3
172-0-0-3.hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.3
_http._tcp.hdls1.testns.svc.clusterset.local.	5	IN	SRV	0 16 80 172-0-0-3.hdls1.testns.svc.clusterset.local.
hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.4
dup-name.hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.4
_http._tcp.hdls1.testns.svc.clusterset.local.	5	IN	SRV	0 16 80 dup-name.hdls1.testns.svc.clusterset.local.
hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.5
dup-name.hdls1.testns.svc.clusterset.local.	5	IN	A	172.0.0.5
_http._tcp.hdls1.testns.svc.clusterset.local.	5	IN	SRV	0 16 80 dup-name.hdls1.testns.svc.clusterset.local.
hdls1.testns.svc.clusterset.local.	5	IN	AAAA	5678:abcd::1
5678-abcd--1.hdls1.testns.svc.clusterset.local.	5	IN	AAAA	5678:abcd::1
_http._tcp.hdls1.testns.svc.clusterset.local.	5	IN	SRV	0 16 80 5678-abcd--1.hdls1.testns.svc.clusterset.local.
hdls1.testns.svc.clusterset.local.	5	IN	AAAA	5678:abcd::2
5678-abcd--2.hdls1.testns.svc.clusterset.local.	5	IN	AAAA	5678:abcd::2
_http._tcp.hdls1.testns.svc.clusterset.local.	5	IN	SRV	0 16 80 5678-abcd--2.hdls1.testns.svc.clusterset.local.
hdlsprtls.testns.svc.clusterset.local.	5	IN	A	172.0.0.20
172-0-0-20.hdlsprtls.testns.svc.clusterset.local.	5	IN	A	172.0.0.20
kubedns.kube-system.svc.clusterset.local.	5	IN	A	10.0.0.10
kubedns.kube-system.svc.clusterset.local.	5	IN	SRV	0 100 53 kubedns.kube-system.svc.clusterset.local.
_dns._udp.kubedns.kube-system.svc.clusterset.local.	5	IN	SRV	0 100 53 kubedns.kube-system.svc.clusterset.local.
svc-dual-stack.testns.svc.clusterset.local.	5	IN	A	10.0.0.3
svc-dual-stack.testns.svc.clusterset.local.	5	IN	AAAA	10::3
svc-dual-stack.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svc-dual-stack.testns.svc.clusterset.local.
_http._tcp.svc-dual-stack.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svc-dual-stack.testns.svc.clusterset.local.
svc1.testns.svc.clusterset.local.	5	IN	A	10.0.0.1
svc1.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svc1.testns.svc.clusterset.local.
_http._tcp.svc1.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svc1.testns.svc.clusterset.local.
svc6.testns.svc.clusterset.local.	5	IN	AAAA	1234:abcd::1
svc6.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svc6.testns.svc.clusterset.local.
_http._tcp.svc6.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svc6.testns.svc.clusterset.local.
svcempty.testns.svc.clusterset.local.	5	IN	A	10.0.0.1
svcempty.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svcempty.testns.svc.clusterset.local.
_http._tcp.svcempty.testns.svc.clusterset.local.	5	IN	SRV	0 100 80 svcempty.testns.svc.clusterset.local.
clusterset.local.	5	IN	SOA	ns.dns.clusterset.local. hostmaster.clusterset.local. 3 7200 1800 86400 5
`
