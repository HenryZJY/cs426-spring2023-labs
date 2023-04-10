## 3A-2
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

In conclusion, they are note guaranteed to resolve all possible leader election failure scenarios. 