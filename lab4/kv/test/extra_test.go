package kvtest

import (
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// Like previous labs, you must write some tests on your own.
// Add your test cases in this file and submit them as extra_test.go.
// You must add at least 5 test cases, though you can add as many as you like.
//
// You can use any type of test already used in this lab: server
// tests, client tests, or integration tests.
//
// You can also write unit tests of any utility functions you have in utils.go
//
// Tests are run from an external package, so you are testing the public API
// only. You can make methods public (e.g. utils) by making them Capitalized.

func TestServerDeleteNotExistingKey(t *testing.T) {
	setup := MakeTestSetup(MakeBasicOneShard())

	err := setup.NodeDelete("n1", "nonexistent_key")
	assert.Nil(t, err)

	_, wasFound, err := setup.NodeGet("n1", "nonexistent_key")
	assert.Nil(t, err)
	assert.False(t, wasFound)

	setup.Shutdown()
}

func TestServerSetAndGetMultipleKeys(t *testing.T) {
	setup := MakeTestSetup(MakeBasicOneShard())

	keyValues := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	for key, value := range keyValues {
		err := setup.NodeSet("n1", key, value, 10*time.Second)
		assert.Nil(t, err)
	}

	for key, expectedValue := range keyValues {
		val, wasFound, err := setup.NodeGet("n1", key)
		assert.Nil(t, err)
		assert.True(t, wasFound)
		assert.Equal(t, expectedValue, val)
	}

	setup.Shutdown()
}

func TestClientGetWithTTL(t *testing.T) {
	// Test Get with key-value pair that has a TTL (time to live) set.
	// After the TTL has passed, the value should not be found.
	setup := MakeTestSetupWithoutServers(MakeBasicOneShard())
	setup.clientPool.OverrideSetResponse("n1")

	err := setup.Set("abc123", "value with TTL", 2*time.Second)
	assert.Nil(t, err)

	setup.clientPool.OverrideGetResponse("n1", "value with TTL", true)
	val, wasFound, err := setup.Get("abc123")
	assert.Nil(t, err)
	assert.True(t, wasFound)
	assert.Equal(t, "value with TTL", val)

	time.Sleep(3 * time.Second)

	setup.clientPool.OverrideGetResponse("n1", "", false)
	_, wasFound, err = setup.Get("abc123")
	assert.Nil(t, err)
	assert.False(t, wasFound)
}

func TestIntegrationDelete(t *testing.T) {
	setup := MakeTestSetup(MakeFourNodesWithFiveShards())
	numKeys, keys, vals := initSetup(t, setup)

	for i := 0; i < numKeys; i++ {
		err := setup.Delete(keys[i])
		assert.Nil(t, err)
	}

	cntRes := countKeysFound(t, setup, keys, vals)
	assert.Equal(t, 0, cntRes.numKeysFound)
	assert.Equal(t, numKeys, cntRes.numKeysMissing)

	setup.Shutdown()
}

func TestClientGetMultiShardSingleNode(t *testing.T) {
	// Tests the get function with multiple shards on a single node
	setup := MakeTestSetupWithoutServers(MakeMultiShardSingleNode())

	keys := RandomKeys(100, 5)
	setup.clientPool.OverrideSetResponse("n1")
	for _, key := range keys {
		err := setup.Set(key, "", 1*time.Second)
		assert.Nil(t, err)
	}

	setup.clientPool.OverrideGetResponse("n1", "found!", true)
	for _, key := range keys {
		val, wasFound, err := setup.Get(key)
		assert.Nil(t, err)
		assert.True(t, wasFound)
		assert.Equal(t, "found!", val)
	}

	// should be 100 sets + 100 gets
	assert.Equal(t, 200, setup.clientPool.GetRequestsSent("n1"))
}
