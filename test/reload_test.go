package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"

	"github.com/miekg/dns"
)

func TestReload(t *testing.T) {
	corefile := `.:0 {
		whoami
	}`

	coreInput := NewInput(corefile)

	c, err := CoreDNSServer(corefile)
	if err != nil {
		t.Fatalf("Could not get CoreDNS serving instance: %s", err)
	}

	udp, _ := CoreDNSServerPorts(c, 0)

	send(t, udp)

	c1, err := c.Restart(coreInput)
	if err != nil {
		t.Fatal(err)
	}
	udp, _ = CoreDNSServerPorts(c1, 0)

	send(t, udp)

	c1.Stop()
}

func send(t *testing.T, server string) {
	t.Helper()
	m := new(dns.Msg)
	m.SetQuestion("whoami.example.org.", dns.TypeSRV)

	r, err := dns.Exchange(m, server)
	if err != nil {
		// This seems to fail a lot on travis, quick'n dirty: redo
		r, err = dns.Exchange(m, server)
		if err != nil {
			return
		}
	}
	if r.Rcode != dns.RcodeSuccess {
		t.Fatalf("Expected successful reply, got %s", dns.RcodeToString[r.Rcode])
	}
	if len(r.Extra) != 2 {
		t.Fatalf("Expected 2 RRs in additional, got %d", len(r.Extra))
	}
}

func TestReloadHealth(t *testing.T) {
	corefile := `.:0 {
		health 127.0.0.1:52182
		whoami
	}`

	c, err := CoreDNSServer(corefile)
	if err != nil {
		if strings.Contains(err.Error(), inUse) {
			return // meh, but don't error
		}
		t.Fatalf("Could not get service instance: %s", err)
	}

	if c1, err := c.Restart(NewInput(corefile)); err != nil {
		t.Fatal(err)
	} else {
		c1.Stop()
	}
}

func TestReloadMetricsHealth(t *testing.T) {
	corefile := `.:0 {
		prometheus 127.0.0.1:53183
		health 127.0.0.1:53184
		whoami
	}`

	c, err := CoreDNSServer(corefile)
	if err != nil {
		if strings.Contains(err.Error(), inUse) {
			return // meh, but don't error
		}
		t.Fatalf("Could not get service instance: %s", err)
	}

	c1, err := c.Restart(NewInput(corefile))
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Stop()

	// Health
	resp, err := http.Get("http://localhost:53184/health")
	if err != nil {
		t.Fatal(err)
	}
	ok, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(ok) != http.StatusText(http.StatusOK) {
		t.Errorf("Failed to receive OK, got %s", ok)
	}

	// Metrics
	resp, err = http.Get("http://localhost:53183/metrics")
	if err != nil {
		t.Fatal(err)
	}
	const proc = "coredns_build_info"
	metrics, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(metrics, []byte(proc)) {
		t.Errorf("Failed to see %s in metric output", proc)
	}
}

func collectMetricsInfo(addr string, procs ...string) error {
	cl := &http.Client{}
	resp, err := cl.Get(fmt.Sprintf("http://%s/metrics", addr))
	if err != nil {
		return err
	}
	metrics, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, p := range procs {
		if !bytes.Contains(metrics, []byte(p)) {
			return fmt.Errorf("failed to see %s in metric output \n%s", p, metrics)
		}
	}
	return nil
}

// TestReloadSeveralTimeMetrics ensures that metrics are not pushed to
// prometheus once the metrics plugin is removed and a coredns
// reload is triggered
// 1. check that metrics have not been exported to prometheus before coredns starts
// 2. ensure that build-related metrics have been exported once coredns starts
// 3. trigger multiple reloads without changing the corefile
// 4. remove the metrics plugin and trigger a final reload
// 5. ensure the original prometheus exporter has not received more metrics
func TestReloadSeveralTimeMetrics(t *testing.T) {
	//TODO: add a tool that find an available port because this needs to be a port
	// that is not used in another test
	promAddress := "127.0.0.1:53185"
	proc := "coredns_build_info"
	corefileWithMetrics := `.:0 {
		prometheus ` + promAddress + `
		whoami
	}`
	corefileWithoutMetrics := `.:0 {
		whoami
	}`

	if err := collectMetricsInfo(promAddress, proc); err == nil {
		t.Errorf("Prometheus is listening before the test started")
	}
	serverWithMetrics, err := CoreDNSServer(corefileWithMetrics)
	if err != nil {
		if strings.Contains(err.Error(), inUse) {
			return
		}
		t.Errorf("Could not get service instance: %s", err)
	}
	// verify prometheus is running
	if err := collectMetricsInfo(promAddress, proc); err != nil {
		t.Errorf("Prometheus is not listening : %s", err)
	}
	reloadCount := 2
	for i := range reloadCount {
		serverReload, err := serverWithMetrics.Restart(
			NewInput(corefileWithMetrics),
		)
		if err != nil {
			t.Errorf("Could not restart CoreDNS : %s, at loop %v", err, i)
		}
		if err := collectMetricsInfo(promAddress, proc); err != nil {
			t.Errorf("Prometheus is not listening : %s", err)
		}
		serverWithMetrics = serverReload
	}
	// reload without prometheus
	serverWithoutMetrics, err := serverWithMetrics.Restart(
		NewInput(corefileWithoutMetrics),
	)
	if err != nil {
		t.Errorf("Could not restart a second time CoreDNS : %s", err)
	}
	serverWithoutMetrics.Stop()
	// verify that metrics have not been pushed
	if err := collectMetricsInfo(promAddress, proc); err == nil {
		t.Errorf("Prometheus is still listening")
	}
}

func TestMetricsAvailableAfterReload(t *testing.T) {
	//TODO: add a tool that find an available port because this needs to be a port
	// that is not used in another test
	promAddress := "127.0.0.1:53186"
	procMetric := "coredns_build_info"
	procCache := "coredns_cache_entries"
	procForward := "coredns_dns_request_duration_seconds"
	corefileWithMetrics := `.:0 {
		prometheus ` + promAddress + `
		cache
		forward . 8.8.8.8 {
			force_tcp
		}
	}`

	inst, _, tcp, err := CoreDNSServerAndPorts(corefileWithMetrics)
	if err != nil {
		if strings.Contains(err.Error(), inUse) {
			return
		}
		t.Errorf("Could not get service instance: %s", err)
	}
	// send a query and check we can scrap corresponding metrics
	cl := dns.Client{Net: "tcp"}
	m := new(dns.Msg)
	m.SetQuestion("www.example.org.", dns.TypeA)

	if _, _, err := cl.Exchange(m, tcp); err != nil {
		t.Fatalf("Could not send message: %s", err)
	}

	// we should have metrics from forward, cache, and metrics itself
	if err := collectMetricsInfo(promAddress, procMetric, procCache, procForward); err != nil {
		t.Errorf("Could not scrap one of expected stats : %s", err)
	}

	// now reload
	instReload, err := inst.Restart(
		NewInput(corefileWithMetrics),
	)
	if err != nil {
		t.Errorf("Could not restart CoreDNS : %s", err)
		instReload = inst
	}

	// check the metrics are available still
	if err := collectMetricsInfo(promAddress, procMetric, procCache, procForward); err != nil {
		t.Errorf("Could not scrap one of expected stats : %s", err)
	}

	instReload.Stop()
	// verify that metrics have not been pushed
}

func TestMetricsAvailableAfterReloadAndFailedReload(t *testing.T) {
	//TODO: add a tool that find an available port because this needs to be a port
	// that is not used in another test
	promAddress := "127.0.0.1:53187"
	procMetric := "coredns_build_info"
	procCache := "coredns_cache_entries"
	procForward := "coredns_dns_request_duration_seconds"
	corefileWithMetrics := `.:0 {
		prometheus ` + promAddress + `
		cache
		forward . 8.8.8.8 {
			force_tcp
		}
	}`
	invalidCorefileWithMetrics := `.:0 {
		prometheus ` + promAddress + `
		cache
		forward . 8.8.8.8 {
			force_tcp
		}
		invalid
	}`

	inst, _, tcp, err := CoreDNSServerAndPorts(corefileWithMetrics)
	if err != nil {
		if strings.Contains(err.Error(), inUse) {
			return
		}
		t.Errorf("Could not get service instance: %s", err)
	}
	// send a query and check we can scrap corresponding metrics
	cl := dns.Client{Net: "tcp"}
	m := new(dns.Msg)
	m.SetQuestion("www.example.org.", dns.TypeA)

	if _, _, err := cl.Exchange(m, tcp); err != nil {
		t.Fatalf("Could not send message: %s", err)
	}

	// we should have metrics from forward, cache, and metrics itself
	if err := collectMetricsInfo(promAddress, procMetric, procCache, procForward); err != nil {
		t.Errorf("Could not scrap one of expected stats : %s", err)
	}

	for range 2 {
		// now provide a failed reload
		invInst, err := inst.Restart(
			NewInput(invalidCorefileWithMetrics),
		)
		if err == nil {
			t.Errorf("Invalid test - this reload should fail")
			inst = invInst
		}
	}

	// now reload with correct corefile
	instReload, err := inst.Restart(
		NewInput(corefileWithMetrics),
	)
	if err != nil {
		t.Errorf("Could not restart CoreDNS : %s", err)
		instReload = inst
	}

	// check the metrics are available still
	if err := collectMetricsInfo(promAddress, procMetric, procCache, procForward); err != nil {
		t.Errorf("Could not scrap one of expected stats : %s", err)
	}

	instReload.Stop()
	// verify that metrics have not been pushed
}

// TestReloadUnreadyPlugin tests that the ready plugin properly resets the list of readiness implementors during a reload.
// If it fails to do so, ready will respond with duplicate plugin names after a reload (e.g. in this test "unready,unready").
func TestReloadUnreadyPlugin(t *testing.T) {
	// Add/Register a perpetually unready plugin
	dnsserver.Directives = append([]string{"unready"}, dnsserver.Directives...)
	u := new(unready)
	plugin.Register("unready", func(c *caddy.Controller) error {
		dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
			u.next = next
			return u
		})
		return nil
	})

	corefile := `.:0 {
		unready
        whoami
        ready 127.0.0.1:53185
	}`

	coreInput := NewInput(corefile)

	c, err := CoreDNSServer(corefile)
	if err != nil {
		t.Fatalf("Could not get CoreDNS serving instance: %s", err)
	}

	c1, err := c.Restart(coreInput)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get("http://127.0.0.1:53185/ready")
	if err != nil {
		t.Fatal(err)
	}
	bod, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(bod) != u.Name() {
		t.Errorf("Expected /ready endpoint response body %q, got %q", u.Name(), bod)
	}

	c1.Stop()
}

type unready struct {
	next plugin.Handler
}

func (u *unready) Ready() bool { return false }

func (u *unready) Name() string { return "unready" }

func (u *unready) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	return u.next.ServeDNS(ctx, w, r)
}

const inUse = "address already in use"
