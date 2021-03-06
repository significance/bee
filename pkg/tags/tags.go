// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package tags

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"github.com/ethersphere/bee/pkg/logging"
	"github.com/ethersphere/bee/pkg/storage"
	"github.com/ethersphere/bee/pkg/swarm"
)

var (
	TagUidFunc  = rand.Uint32
	ErrNotFound = errors.New("tag not found")
)

// Tags hold tag information indexed by a unique random uint32
type Tags struct {
	tags       *sync.Map
	stateStore storage.StateStorer
	logger     logging.Logger
}

// NewTags creates a tags object
func NewTags(stateStore storage.StateStorer, logger logging.Logger) *Tags {
	return &Tags{
		tags:       &sync.Map{},
		stateStore: stateStore,
		logger:     logger,
	}
}

// Create creates a new tag, stores it by the name and returns it
// it returns an error if the tag with this name already exists
func (ts *Tags) Create(s string, total int64) (*Tag, error) {
	t := NewTag(context.Background(), TagUidFunc(), s, total, nil, ts.stateStore, ts.logger)

	if _, loaded := ts.tags.LoadOrStore(t.Uid, t); loaded {
		return nil, errExists
	}

	return t, nil
}

// All returns all existing tags in Tags' sync.Map
// Note that tags are returned in no particular order
func (ts *Tags) All() (t []*Tag) {
	ts.tags.Range(func(k, v interface{}) bool {
		t = append(t, v.(*Tag))

		return true
	})

	return t
}

// Get returns the underlying tag for the uid or an error if not found
func (ts *Tags) Get(uid uint32) (*Tag, error) {
	t, ok := ts.tags.Load(uid)
	if !ok {
		// see if the tag is present in the store
		// if yes, load it in to the memory
		ta, err := ts.getTagFromStore(uid)
		if err != nil {
			return nil, ErrNotFound
		}
		ts.tags.LoadOrStore(ta.Uid, ta)
		return ta, nil
	}
	return t.(*Tag), nil
}

// GetByAddress returns the latest underlying tag for the address or an error if not found
func (ts *Tags) GetByAddress(address swarm.Address) (*Tag, error) {
	var t *Tag
	var lastTime time.Time
	ts.tags.Range(func(key interface{}, value interface{}) bool {
		rcvdTag := value.(*Tag)
		if rcvdTag.Address.Equal(address) && rcvdTag.StartedAt.After(lastTime) {
			t = rcvdTag
			lastTime = rcvdTag.StartedAt
		}
		return true
	})

	if t == nil {
		return nil, ErrNotFound
	}
	return t, nil
}

// Range exposes sync.Map's iterator
func (ts *Tags) Range(fn func(k, v interface{}) bool) {
	ts.tags.Range(fn)
}

func (ts *Tags) Delete(k interface{}) {
	ts.tags.Delete(k)
}

func (ts *Tags) MarshalJSON() (out []byte, err error) {
	m := make(map[string]*Tag)
	ts.Range(func(k, v interface{}) bool {
		key := fmt.Sprintf("%d", k)
		val := v.(*Tag)

		// don't persist tags which were already done
		if !val.Done(StateSynced) {
			m[key] = val
		}
		return true
	})
	return json.Marshal(m)
}

func (ts *Tags) UnmarshalJSON(value []byte) error {
	m := make(map[string]*Tag)
	err := json.Unmarshal(value, &m)
	if err != nil {
		return err
	}
	for k, v := range m {
		key, err := strconv.ParseUint(k, 10, 32)
		if err != nil {
			return err
		}

		// prevent a condition where a chunk was sent before shutdown
		// and the node was turned off before the receipt was received
		v.Sent = v.Synced

		ts.tags.Store(key, v)
	}

	return err
}

// getTagFromStore get a given tag from the state store.
func (ts *Tags) getTagFromStore(uid uint32) (*Tag, error) {
	key := "tags_" + strconv.Itoa(int(uid))
	var data []byte
	err := ts.stateStore.Get(key, &data)
	if err != nil {
		return nil, err
	}
	var ta Tag
	err = ta.UnmarshalBinary(data)
	if err != nil {
		return nil, err
	}
	return &ta, nil
}

// Close is called when the node goes down. This is when all the tags in memory is persisted.
func (ts *Tags) Close() (err error) {
	// store all the tags in memory
	tags := ts.All()
	for _, t := range tags {
		ts.logger.Trace("updating tag: ", t.Uid)
		err := t.saveTag()
		if err != nil {
			return err
		}
	}
	return nil
}
