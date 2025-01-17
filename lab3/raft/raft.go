raft/my_raft_test.go                                                                                000644  000765  000024  00000005401 14400732306 016537  0                                                                                                    ustar 00junyanzhang                     staff                           000000  000000                                                                                                                                                                         package raft

import (
	"testing"
	"math/rand"
	"time"
	"sync"
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

func TestMoreUnreliable(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, true, false)
	defer cfg.cleanup()

	cfg.begin("Test (My test 4): More unreliable")

	cfg.one(101, 5, true)
	var wg sync.WaitGroup

	for iters := 1; iters < 50; iters++ {
		for j := 0; j < 4; j++ {
			wg.Add(1)
			go func(iters, j int) {
				defer wg.Done()
				cfg.one((100*iters)+j, 1, true)
			}(iters, j)
		}
		cfg.one(iters, 1, true)
	}
	wg.Wait()
	cfg.end()
}

func TestLeaderRejoin(t *testing.T) {
	servers := 5
	cfg := make_config(t, servers, false, false)
	defer cfg.cleanup()

	cfg.begin("Test (My test 5): Leader rejoins")

	cfg.one(101, 5, true)
	leader := cfg.checkOneLeader()
	cfg.disconnect(leader)
	cfg.one(102, 4, true)
	cfg.rafts[leader].Start(103)
	cfg.rafts[leader].Start(105)
	cfg.checkOneLeader()
	cfg.connect(leader)
	cfg.rafts[leader].Start(106)
	cfg.one(103, 5, true)
	cfg.checkOneLeader()

	cfg.end()
}                                                                                                                                                                                                                                                               raft/my_util.go                                                                                     000644  000765  000024  00000014767 14400732002 015531  0                                                                                                    ustar 00junyanzhang                     staff                           000000  000000                                                                                                                                                                         package raft

import (
	"time"
)

type AppendEntriesArgs struct {
	Term         int
	LeaderId     int
	PrevLogIndex int
	PrevLogTerm  int
	Entries      []LogEntry
	LeaderCommit int
}

type AppendEntriesReply struct {
	Term    int
	Success bool
	NexIndex int
}

func (rf *Raft) resetAllHeartbeatTimer() {
	for i, _ := range rf.appendEntriesTimers {
		rf.appendEntriesTimers[i].Stop()
		rf.appendEntriesTimers[i].Reset(0)
	}
}

func (rf *Raft) resetHeartbeatTimer(peeridx int) {
	rf.appendEntriesTimers[peeridx].Stop()
	rf.appendEntriesTimers[peeridx].Reset(HeartbeatTimeout)
}


func (rf *Raft) makeAppendEntriesArgs(peeridx int) AppendEntriesArgs {
	// Make the logentries to append
	nextIdx := rf.nextIndex[peeridx]
	lastLogTerm, lastLogIndex := rf.getLastLogTermIndex()
	logEntries := make([]LogEntry, 0)
	var prevLogIndex int
	var prevLogTerm int

	if nextIdx > lastLogIndex {
		// No log to send
		rf.delog("makeAppendEntriesArgs: no log to send")
		prevLogIndex = lastLogIndex
		prevLogTerm = lastLogTerm
	} else {
		logEntries = append(logEntries, rf.logEntries[rf.getIdxByLogIndex(nextIdx):]...)
		prevLogIndex = nextIdx - 1
		
		prevLogTerm = rf.getLogEntryByIndex(prevLogIndex).Term

	}

	args := AppendEntriesArgs{
		Term:         rf.currentTerm,
		LeaderId:     rf.me,
		LeaderCommit: rf.commitIndex,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      logEntries,
	}
	return args
}

func (rf *Raft) sendAppendEntries(peeridx int) {
	RPCTimer := time.NewTimer(RPCTimeout)
	defer RPCTimer.Stop()
	for !rf.killed() {
		rf.lock("sendAppendEntries1")
		if rf.role != Leader {
			rf.resetHeartbeatTimer(peeridx)
			rf.unlock("sendAppendEntries1")
			return
		}
		rf.resetHeartbeatTimer(peeridx)
		args := rf.makeAppendEntriesArgs(peeridx)
		rf.unlock("sendAppendEntries1")
		RPCTimer.Stop()
		RPCTimer.Reset(RPCTimeout)
		reply := AppendEntriesReply{}
		resCh := make(chan bool, 1)
		go func(args *AppendEntriesArgs, reply *AppendEntriesReply) {
			ok := rf.peers[peeridx].Call("Raft.AppendEntries", args, reply)
			if !ok {
				time.Sleep(10 * time.Millisecond)
			}
			resCh <- ok
		}(&args, &reply)

		select {
		case <-RPCTimer.C:
			rf.delog("sendAppendEntries timeout: peeridx=%v", peeridx)
			continue
		case <-rf.stopChannel:
			rf.delog("sendAppendEntries: stopped")
			return
		case ok := <-resCh:
			if !ok {
				rf.delog("sendAppendEntries: GGGGGGG")
				continue
			}
		}

		rf.delog("sendAppendEntries: peeridx=%v, args=%+v, reply=%+v", peeridx, args, reply)
		// handle the reply
		rf.lock("sendAppendEntries2")
		if rf.currentTerm < reply.Term {
			// stfu
			rf.delog("WAS SHUSHED STFU")
			rf.currentTerm = reply.Term
			rf.changeRole(Follower)
			rf.resetElectionTimer()
			rf.persist()
			rf.unlock("sendAppendEntries2")
			return
		}
		if rf.role != Leader || rf.currentTerm != args.Term {
			// stfu
			rf.delog("WEIRD STFU")
			rf.unlock("sendAppendEntries2")
			return
		}
		if reply.Success {
			if rf.nextIndex[peeridx] < reply.NexIndex {
				rf.nextIndex[peeridx] = reply.NexIndex
				rf.matchIndex[peeridx] = reply.NexIndex - 1
			}
			if len(args.Entries) > 0 && rf.currentTerm == args.Entries[len(args.Entries)-1].Term {
				rf.updateCommitIndex()
			}
			rf.persist()
			rf.unlock("sendAppendEntries2")
			return
		} else { // reply False
			if reply.NexIndex != 0 { // reply.NexIndex == 0 means the follower's log is empty
				if 0 < reply.NexIndex {
					rf.nextIndex[peeridx] = reply.NexIndex
					rf.unlock("sendAppendEntries2")
					rf.delog("Retrying sendAppendEntries")
					continue
				}
			} else {
				rf.unlock("sendAppendEntries2")
			}
		}
	}
}

func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	rf.lock("AppendEntries")
	rf.delog("AppendEntries: args=%+v", args)
	reply.Term = rf.currentTerm

	if args.Term < rf.currentTerm {
		reply.Success = false
		rf.unlock("AppendEntries")
		return
	}
	rf.currentTerm = args.Term
	rf.changeRole(Follower)
	rf.resetElectionTimer()

	_, lastLogIndex := rf.getLastLogTermIndex()
	if args.PrevLogIndex > lastLogIndex{ // Missing logs in the middle
		rf.delog("AppendEntries: Missing logs in the middle, args.PrevLogIndex=%v, lastLogIndex=%v", args.PrevLogIndex, lastLogIndex)
		reply.Success = false
		reply.NexIndex = lastLogIndex + 1
	} else if args.PrevLogIndex == 0 { // Can fill the gap
		if rf.isArgsEntriesOutOfOrder(args) {
			reply.Success = false
			reply.NexIndex = 0
		} else {
			reply.Success = true
			rf.delog("AppendEntries: successfully filled the gap, Now i have %+v log entries", len(rf.logEntries))
			rf.logEntries = append(rf.logEntries[:1], args.Entries...)
			_, tempLastIndex := rf.getLastLogTermIndex()
			reply.NexIndex = tempLastIndex + 1
		}
	} else if rf.logEntries[rf.getIdxByLogIndex(args.PrevLogIndex)].Term == args.PrevLogTerm {
		if rf.isArgsEntriesOutOfOrder(args) {
			reply.Success = false
			reply.NexIndex = 0
		} else { // Can fill the gap with the log entries in args
			reply.Success = true
			rf.delog("AppendEntries: successfully filled the gap with the log entries in args")
			rf.logEntries = append(rf.logEntries[:rf.getIdxByLogIndex(args.PrevLogIndex)+1], args.Entries...)
			_, tempLastIndex := rf.getLastLogTermIndex()
			reply.NexIndex = tempLastIndex + 1
		}
	} else {
		// Need to find the last index of the previous term
		reply.Success = false
		idx := args.PrevLogIndex
		argTerm := rf.logEntries[rf.getIdxByLogIndex(args.PrevLogIndex)].Term
		for idx > rf.commitIndex && idx > 0 && rf.logEntries[rf.getIdxByLogIndex(idx)].Term == argTerm {
			idx--
		}
		reply.NexIndex = idx + 1
		rf.delog("AppendEntries logs dont match")
	}

	if reply.Success && args.LeaderCommit > rf.commitIndex {
		rf.commitIndex = args.LeaderCommit
		rf.signalApplyCh <- struct{}{}
	}


	rf.persist()
	rf.delog("Handle AppendEntries: reply=%+v", reply)
	rf.unlock("AppendEntries")
}

func (rf *Raft) updateCommitIndex() {
	rf.delog("Start in updateCommitIndex()")
	updated := false
	for i := rf.commitIndex + 1; i <= len(rf.logEntries); i++ {
		count := 0
		for _, matchIndex := range rf.matchIndex {
			if matchIndex >= i {
				count++
				if count > len(rf.peers)/2 {
					rf.delog("Updating commit index: %d.", i)
					rf.commitIndex = i
					updated = true
					break
				}
			}
		}
		if rf.commitIndex != i{
			break
		}
	}
	if updated {
		rf.signalApplyCh <- struct{}{}
	}
	rf.delog("updateCommitIndex() returns")
}

func (rf *Raft) isArgsEntriesOutOfOrder(args *AppendEntriesArgs) bool {
	lastLogTerm, lastLogIndex := rf.getLastLogTermIndex()
	if args.PrevLogIndex + len(args.Entries) < lastLogIndex && lastLogTerm == args.Term {
		return true
	}
	return false
}         ._discussions.md                                                                                    000644  000765  000024  00000000334 14401727733 015672  0                                                                                                    ustar 00junyanzhang                     staff                           000000  000000                                                                                                                                                                             Mac OS X            	   2   �      �                                      ATTR       �   �   H                  �   H  com.apple.macl    h�^:k�B�˛��                                                                                                                                                                                                                                                                                                                                                          PaxHeader/discussions.md                                                                            000644  000765  000024  00000000414 14401727733 017425  x                                                                                                    ustar 00junyanzhang                     staff                           000000  000000                                                                                                                                                                         30 mtime=1678225371.859144544
133 LIBARCHIVE.xattr.com.apple.macl=BABo0V46a6ZCFJHLm5/rHcKMAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
105 SCHILY.xattr.com.apple.macl= h�^:k�B�˛��                                                      
                                                                                                                                                                                                                                                    discussions.md                                                                                      000644  000765  000024  00000006107 14401727733 015461  0                                                                                                    ustar 00junyanzhang                     staff                           000000  000000                                                                                                                                                                         ## 3A-2
### Q 1
Let's consider a scenario with a Raft cluster of 3 nodes. In this scenario, we assume that all nodes have started and are communicating with each other.

Initially, all nodes are in the follower state, and node 1 starts an election process by incrementing its current term and transitioning to the candidate state. It sends out RequestVote RPCs to the other nodes, asking for their votes.

Before any node can respond to the RequestVote RPC, a network partition occurs, separating the nodes into two groups: node 1 is on one side of the partition, while nodes 2 and 3 are on the other side.

Node 1 cannot receive any responses from nodes 2 and 3 because of the network partition. After the election timeout, node 1 starts a new election process by incrementing its current term again and transitioning to a candidate state. It sends out RequestVote RPCs to the other nodes on its side of the partition.

Nodes 2 and 3 also start their own elections, incrementing their current terms and transitioning to candidate states. They send out RequestVote RPCs to each other, but since they are on the same side of the partition, they do not receive any responses from node 1.

As a result, none of the nodes receive a majority of votes, and no leader can be elected. This situation can persist indefinitely, as the nodes continue to try and fail to elect a leader.

### Q 2
In practice, Raft avoids the scenario where leader election fails to elect a leader by using a randomized election timeout mechanism. This avoids the scenario where multiple nodes start their elections at the same time which leads to vote splitting and a failure to elect a leader.

The randomized election timeout mechanism ensures that, over time, each candidate node gets a chance to become a leader. Nodes with shorter timeouts will become candidates more frequently and therefore have a higher chance of becoming a leader, and vice versa. 

In addition, Raft leader sends periodic heartbeats to follower nodes to indicate its presence. 

### Sources:
- https://medium.com/yugabyte/low-latency-reads-in-geo-distributed-sql-with-raft-leader-leases-9740a38246d1

## ExtraCredit1
If the Raft instances implement Pre-Vote and CheckQuorum, the scenario I constructed above may still not resolve if there is a network partition that separates the nodes. In this case, the cluster may not have a majority of nodes on either side of the partition, and the Pre-Vote and CheckQuorum mechanisms may not be able to resolve the election deadlock. 

For example, let's say we have a Raft cluster of five nodes, and a network partition separates the cluster into two parts, with two nodes on one side and three nodes on the other side. The two nodes on one side may elect a leader using the Pre-Vote mechanism, while the three nodes on the other side may also elect a leader using the same mechanism. Since neither side has a majority of nodes, the CheckQuorum mechanism will not be able to resolve the election deadlock, and the cluster may be stuck without a leader. 

In conclusion, they are note guaranteed to resolve all possible leader election failure scenarios.                                                                                                                                                                                                                                                                                                                                                                                                                                                          time.log                                                                                            000644  000765  000024  00000003031 14401730134 014210  0                                                                                                    ustar 00junyanzhang                     staff                           000000  000000                                                                                                                                                                         ESTIMATE of time to complete assignment: 30 hours

      Time     Time
Date  Started  Spent Work completed
----  -------  ----  --------------
02/20 6:20 pm  2:00 Read lab specification
02/21 7:00 pm  2:00 Read raft extended paper and make notes & mind map
02/22 7:20 pm  4:00 Finished reading paper and started part A
02/24 7:00 pm  2:00 Writing Part A
02/25 2:00 pm  3:00 Finished writing Aart A and debugged for a while. 
02/26 7:00 pm  3:00 Writing Part B
02/27 9:00 pm   4:00 Writing Part B, debugging failure of agreement
02/28 10:00 am  2:30 Wrote and debugged part B, but two tests still fail
02/28 3:20 pm   1:00 Part B tests passed
03/01 9:00 pm   2:30 Writing Part C
03/02 10:00 pm  2:00 Writing tests and fixed bugs
03/03 6:00 pm  2:00  Wrote more tests
03/04 3:30 pm  1:15  Wrote discussion
03/07 4:40 pm  00:30 Finishing up and submit
              ------
              31:15 TOTAL time spent

I discussed my solution with Xianglong Li. 

I encountered failures in tests in both Part A and Part B. In Part A, I encountered
 a problem of "at least one leader" during testing. I spent quite a lot amount of time 
 going through the debug logs that I created to find that I forgot to clear a channel
 in the code. In Part B, I encountered a problem of "failure to reach agreement" during testing. 
 I spent the most time debugging this issue. I had to go through three major bugs in my codes
 to fix this issue. Again, it involves a lot of debugging logs to closely examine. The final bug
 turns out to be a simple reversed condition in the code. :(
                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       