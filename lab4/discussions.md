### Group work

### A4
In our design, we make each shard on the server an independent data strucutre, and each has its own goroutine to clean up the expired keys. It goes through the keys and cleans 
every one second. In terms of number keys, it is linear runtime. Clearly, we have a good number of goroutines spawned.

If the service is write-intensive, we can decrease cleanup frequency and rely more on the checks in the Get() function. We can even move the delete logic entirely into the Get() function. Alternatively, we can partition the shards based on TTL. 

### B2
We use a random assignment in load balancing. Some flaws include:
- The loads are not evenly distributed. This causes a waste of resources. 
- Different server capacities are ignored. 
- There is no support for session persistance. 
We can make a probability distribution based on server capacity and their resource utilized. 

### B3
This strategy could increase average latency for clients. Also, each server can be bombarded with a lot of unnecessary requests. You can assign the nodes a probaility based on their chances of success. 

### B4
Partial failures will result in inconsistencies in the shards stored across nodes. A client can observe different values for Get() on the same key. 

### D2
- Experiment1: go run cmd/stress/tester.go --shardmap shardmaps/test-2-node-full.json
Stress test completed!
Get requests: 6020/6020 succeeded = 100.000000% success rate
Set requests: 1820/1820 succeeded = 100.000000% success rate
Correct responses: 6019/6019 = 100.000000%
Total requests: 7840 = 130.658610 QPS

- Experiment2: go run cmd/stress/tester.go --shardmap shardmaps/test-5-node.json
Stress test completed!
Get requests: 5780/6020 succeeded = 96.013289% success rate
Set requests: 1754/1820 succeeded = 96.373626% success rate
Correct responses: 5777/5778 = 99.982693%
Total requests: 7840 = 130.658244 QPS

While the experiment is running, we deleted a few entries in the shard map json file. We observed that when we deleted every node in one shard, the server returned failures. When we deleted only some of the nodes, the server could still return success. Interestingly, after we deleted all the nodes in the shard, it returned to be successful when we add them back. 