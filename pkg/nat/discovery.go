package nat

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/pion/ice/v3"
	"github.com/pion/stun/v2"
)

const timeout = 30 * time.Second

func FindLocalAddressAndPort(ctx context.Context) (string, int, error) {
	log := logr.FromContextOrDiscard(ctx)

	agent, err := ice.NewAgent(&ice.AgentConfig{
		Urls: []*stun.URI{
			{
				Scheme: stun.SchemeTypeSTUN,
				Host:   "stun.cloudflare.com",
				Port:   3478,
			},
			{
				Scheme: stun.SchemeTypeSTUN,
				Host:   "stun.l.google.com",
				Port:   19302,
			},
			// TODO make configurable?
		},
		NetworkTypes:   []ice.NetworkType{ice.NetworkTypeUDP4},
		CandidateTypes: []ice.CandidateType{ice.CandidateTypeServerReflexive},
	})
	if err != nil {
		return "", 0, fmt.Errorf("unable to initialize NAT discovery client: %w", err)
	}

	defer func() {
		if err := agent.Close(); err != nil {
			log.Error(err, "error closing NAT discovery client")
		}
	}()

	candidates := make(chan ice.Candidate, 1)

	err = agent.OnCandidate(func(candidate ice.Candidate) {
		if candidate != nil {
			log.V(1).Info("NAT discovery candidate", "candidate", candidate.String())
			candidates <- candidate
		}
	})

	if err != nil {
		return "", 0, fmt.Errorf("no proxy candidates found: %w", err)
	}

	if err := agent.GatherCandidates(); err != nil {
		return "", 0, fmt.Errorf("no proxy candidates found: %w", err)
	}

	select {
	case candidate := <-candidates:
		return candidate.Address(), candidate.Port(), nil
	case <-time.After(timeout):
		return "", 0, fmt.Errorf("no proxy candidates found: timeout after %s", timeout.String())
	}
}
