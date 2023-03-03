package raft

import (
	"testing"
	"math/rand"
	"time"
)

func TestElectionWithDisconnectAndCrash(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (My test 1): leader disconnects, one follower crashes, leader restarts")

	cfg.one(101, 5, true)
	leader := cfg.checkOneLeader()

	cfg.disconnect(leader)
	cfg.one(102, 4, true)
	old_leader := cfg.checkOneLeader()
	cfg.crash1((old_leader + 0) % servers)
	cfg.checkOneLeader()
	cfg.start1((old_leader + 0) % servers, cfg.applier)
	cfg.checkOneLeader()

	cfg.end()
}

func TestTermsWithDisconnectAndCrash(t *testing.T) {
	servers := 7
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (My test 2): Multiple disconnects followed by leader crashes")

	cfg.one(101, 7, true)
	cfg.checkOneLeader()

	iters := 10
	for ii := 1; ii < iters; ii++ {
		// disconnect three nodes
		i1 := rand.Int() % servers
		i2 := rand.Int() % servers
		i3 := rand.Int() % servers
		cfg.disconnect(i1)
		cfg.disconnect(i2)
		cfg.disconnect(i3)

		cfg.checkOneLeader()
		cfg.checkTerms()

		cfg.connect(i1)
		cfg.connect(i2)
		cfg.connect(i3)
	}
	leader := cfg.checkOneLeader()
	cfg.crash1(leader)
	cfg.checkOneLeader()
	cfg.one(108, 6, true)
	cfg.start1(leader, cfg.applier)
	
	cfg.end()
}

func TestTermChanges(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (My test 3): Term changes when leader disconnects")

	cfg.one(101, 5, true)
	iters := 5
	for ii := 1; ii < iters; ii++ {
		term1 := cfg.checkTerms()
		leader := cfg.checkOneLeader()
		cfg.disconnect(leader)
		// cfg.one(102, 4, true)
		cfg.checkOneLeader()
		term2 := cfg.checkTerms()
		if term1 == term2 {
			t.Fatalf("Term did not change when leader disconnected")
		}
		cfg.connect(leader)
		time.Sleep(2 * RaftElectionTimeout)
	}

	cfg.end()
}

