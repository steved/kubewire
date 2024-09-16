package agent

import (
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/steved/kubewire/pkg/config"
)

type fakeIptables struct {
	rules map[string]map[string][]string
}

func (f *fakeIptables) AppendUnique(table string, chain string, rulespec ...string) error {
	t, ok := f.rules[table]
	if !ok {
		t = make(map[string][]string)
		f.rules[table] = t
	}

	t[chain] = append(t[chain], strings.Join(rulespec, " "))

	return nil
}

func (f *fakeIptables) InsertUnique(table string, chain string, pos int, rulespec ...string) error {
	f.rules[table][chain] = slices.Insert(f.rules[table][chain], pos, strings.Join(rulespec, " "))

	return nil
}

func (f *fakeIptables) ChainExists(table string, chain string) (bool, error) {
	t, ok := f.rules[table]
	if !ok {
		return false, nil
	}

	if _, ok := t[chain]; !ok {
		return false, nil
	}

	return true, nil
}

func Test_updateIPTablesRules(t *testing.T) {
	defaultInterface = func() (string, netip.Addr, error) {
		return "eth0", netip.AddrFrom4([4]byte{100, 34, 56, 10}), nil
	}

	cfg := config.Wireguard{LocalOverlayAddress: netip.MustParseAddr("10.1.0.1")}

	tests := []struct {
		name               string
		istioEnabled       bool
		proxyExcludedPorts []string
		existingRules      map[string]map[string][]string
		wantRules          map[string]map[string][]string
		wantErr            bool
	}{
		{
			"basic",
			false,
			nil,
			nil,
			map[string]map[string][]string{
				"nat": {
					"PREROUTING": {
						"-p tcp -i eth0 -j DNAT --to-destination 10.1.0.1",
						"-p tcp -i wg0 --destination 100.34.56.10 -j DNAT --to-destination 10.1.0.1",
					},
					"POSTROUTING": {
						"-p udp -o eth0 -j MASQUERADE",
						"-p tcp -o eth0 -j MASQUERADE",
					},
				},
			},
			false,
		},
		{
			"excluded ports",
			false,
			[]string{"12345", "23456"},
			nil,
			map[string]map[string][]string{
				"nat": {
					"PREROUTING": {
						"-p tcp -i eth0 -m multiport ! --dports 12345,23456 -j DNAT --to-destination 10.1.0.1",
						"-p tcp -i wg0 --destination 100.34.56.10 -j DNAT --to-destination 10.1.0.1",
					},
					"POSTROUTING": {
						"-p udp -o eth0 -j MASQUERADE",
						"-p tcp -o eth0 -j MASQUERADE",
					},
				},
			},
			false,
		},
		{
			"istio",
			true,
			[]string{"12345", "23456"},
			nil,
			map[string]map[string][]string{
				"nat": {
					"PREROUTING": {
						"-p tcp -i eth0 -m multiport ! --dports 12345,23456 -j DNAT --to-destination 10.1.0.1",
						"-p tcp -i wg0 -j DNAT --to-destination 127.0.0.6:15001",
					},
					"POSTROUTING": {
						"-p udp -o eth0 -j MASQUERADE",
					},
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := tt.existingRules
			if rules == nil {
				rules = make(map[string]map[string][]string)
			}

			f := &fakeIptables{rules: rules}

			if err := updateIPTablesRules(cfg, f, "wg0", tt.istioEnabled, tt.proxyExcludedPorts); (err != nil) != tt.wantErr {
				t.Errorf("updateIPTablesRules() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !reflect.DeepEqual(f.rules, tt.wantRules) {
				t.Errorf("updateIPTablesRules() rules = %v, expected %v", f.rules, tt.wantRules)
			}
		})
	}
}
