// Copyright 2021 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the “License”);
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an “AS IS” BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package priorityqueue

import (
	"container/heap"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"antrea.io/antrea/pkg/agent/flowexporter"
)

func TestExpirePriorityQueue(t *testing.T) {
	startTime := time.Now()
	testFlowsWithExpire := map[int][]time.Time{
		0: {startTime.Add(4 * time.Second), startTime.Add(6 * time.Second)},
		1: {startTime.Add(10 * time.Second), startTime.Add(12 * time.Second)},
		2: {startTime.Add(1 * time.Second), startTime.Add(3 * time.Second)},
	}
	// ActiveFlowTimeout and IdleFlowTimeout here can be arbitrary values, as
	// they are only used to construct an expirePriorityqueue, but not involved
	// in the logic to be tested
	testPriorityQueue := NewExpirePriorityQueue(1*time.Second, 1*time.Second)
	for key, value := range testFlowsWithExpire {
		item := &flowexporter.ItemToExpire{
			ActiveExpireTime: value[0],
			IdleExpireTime:   value[1],
			Index:            key,
		}
		testPriorityQueue.items = append(testPriorityQueue.items, item)
		testPriorityQueue.KeyToItem[flowexporter.ConnectionKey{fmt.Sprintf("%d", key)}] = item
	}
	heap.Init(testPriorityQueue)

	// Test WriteItemToQueue
	connKey := flowexporter.ConnectionKey{"3"}
	conn := flowexporter.Connection{}
	testPriorityQueue.WriteItemToQueue(connKey, &conn)
	assert.Equal(t, &conn, testPriorityQueue.KeyToItem[connKey].Conn, "WriteItemToQueue didn't add new conn to map")
	newConn := flowexporter.Connection{}
	testPriorityQueue.WriteItemToQueue(connKey, &newConn)
	assert.Equal(t, &newConn, testPriorityQueue.KeyToItem[connKey].Conn, "WriteItemToQueue didn't overwrite existing conn to map")
	hasOld, hasNew := false, false
	for _, item := range testPriorityQueue.items {
		if item.Conn == &conn {
			hasOld = true
		}
		if item.Conn == &newConn {
			hasNew = true
		}
	}
	assert.False(t, hasOld && hasNew, "WriteItemToQueue shouldn't add two items with same key to heap")

	// Test Remove
	removedItem := testPriorityQueue.Remove(connKey)
	assert.Equal(t, &newConn, removedItem.Conn, "Remove didn't return correct item")
	_, exist := testPriorityQueue.KeyToItem[connKey]
	assert.False(t, exist, "Remove didn't delete KeyToItem entry")
	for _, item := range testPriorityQueue.items {
		if item.Conn == &newConn {
			assert.Fail(t, "Remove didn't delete item from queue")
		}
	}

	// Add new flow to the priority queue
	testFlowsWithExpire[3] = []time.Time{startTime.Add(3 * time.Second), startTime.Add(500 * time.Millisecond)}
	newFlowItem := &flowexporter.ItemToExpire{
		ActiveExpireTime: startTime.Add(3 * time.Second),
		IdleExpireTime:   startTime.Add(500 * time.Millisecond),
	}
	heap.Push(testPriorityQueue, newFlowItem)

	// Test the Peek function
	flowReadyToExpire := testPriorityQueue.Peek()
	assert.Equalf(t, testFlowsWithExpire[3][0], flowReadyToExpire.ActiveExpireTime, "Peek() method returns wrong value")
	assert.Equalf(t, testFlowsWithExpire[3][1], flowReadyToExpire.IdleExpireTime, "Peek() method returns wrong value")

	// Test the Update function
	testPriorityQueue.Update(newFlowItem, startTime.Add(2*time.Second), startTime.Add(4*time.Second))
	testFlowsWithExpire[3] = []time.Time{startTime.Add(2 * time.Second), startTime.Add(4 * time.Second)}
	assert.Equalf(t, testFlowsWithExpire[3][0], newFlowItem.ActiveExpireTime, "Update method doesn't work")
	assert.Equalf(t, testFlowsWithExpire[3][1], newFlowItem.IdleExpireTime, "Update method doesn't work")

	// Test the Pop function
	for testPriorityQueue.Len() > 0 {
		queueLen := testPriorityQueue.Len()
		item := heap.Pop(testPriorityQueue).(*flowexporter.ItemToExpire)
		switch queueLen {
		case 1:
			assert.Equal(t, testFlowsWithExpire[1][0], item.ActiveExpireTime)
			assert.Equal(t, testFlowsWithExpire[1][1], item.IdleExpireTime)
		case 2:
			assert.Equal(t, testFlowsWithExpire[0][0], item.ActiveExpireTime)
			assert.Equal(t, testFlowsWithExpire[0][1], item.IdleExpireTime)
		case 3:
			assert.Equal(t, testFlowsWithExpire[3][0], item.ActiveExpireTime)
			assert.Equal(t, testFlowsWithExpire[3][1], item.IdleExpireTime)
		case 4:
			assert.Equal(t, testFlowsWithExpire[2][0], item.ActiveExpireTime)
			assert.Equal(t, testFlowsWithExpire[2][1], item.IdleExpireTime)
		default:
			t.Fatalf("queue length %v is not valid value", queueLen)
		}
	}
}
