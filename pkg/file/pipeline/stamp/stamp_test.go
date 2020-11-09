// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stamp_test

import (
	"context"
	"io"
	"testing"

	"github.com/ethersphere/bee/pkg/crypto"
	"github.com/ethersphere/bee/pkg/file/pipeline"
	"github.com/ethersphere/bee/pkg/file/pipeline/mock"
	"github.com/ethersphere/bee/pkg/file/pipeline/stamp"
	"github.com/ethersphere/bee/pkg/file/pipeline/store"
	"github.com/ethersphere/bee/pkg/postage"
	mmock "github.com/ethersphere/bee/pkg/statestore/mock"
	stmock "github.com/ethersphere/bee/pkg/storage/mock"
	"github.com/ethersphere/bee/pkg/swarm"
)

func TestStampWriter(t *testing.T) {
	privKey, err := crypto.GenerateSecp256k1Key()
	if err != nil {
		t.Fatal(err)
	}

	owner, err := crypto.NewEthereumAddress(privKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	var (
		signer          = crypto.NewDefaultSigner(privKey)
		mockStore       = stmock.NewStorer()
		ms              = mmock.NewStateStore()
		mockChainWriter = mock.NewChainWriter()
		ctx             = context.Background()
		ps              = postage.NewService(ms, 1)
		writer          = stamp.NewStampWriter(signer, ps, mockChainWriter)
	)

	args := pipeline.PipeWriteArgs{Ref: []byte{1, 2, 3, 4}}
	err := writer.ChainWrite(&args)
	if err := args.Stamp.Valid(args.Ref, owner); err != nil {
		t.Fatal(err)
	}

	if calls := mockChainWriter.ChainWriteCalls(); calls != 1 {
		t.Errorf("wanted 1 ChainWrite call, got %d", calls)
	}
}

// TestSum tests that calling Sum on the store writer results in Sum on the next writer in the chain.
func TestSum(t *testing.T) {
	mockChainWriter := mock.NewChainWriter()
	ctx := context.Background()
	writer := store.NewStampWriter(mockChainWriter)
	_, err := writer.Sum()
	if err != nil {
		t.Fatal(err)
	}
	if calls := mockChainWriter.SumCalls(); calls != 1 {
		t.Fatalf("wanted 1 Sum call but got %d", calls)
	}
}

func newTestStampIssuer(t *testing.T) *postage.StampIssuer {
	t.Helper()
	id := make([]byte, 32)
	_, err := io.ReadFull(crand.Reader, id)
	if err != nil {
		t.Fatal(err)
	}
	st := postage.NewStampIssuer("label", "keyID", id, 16, 8)
	addr := make([]byte, 32)
	for i := 0; i < 1<<8; i++ {
		_, err := io.ReadFull(crand.Reader, addr)
		if err != nil {
			t.Fatal(err)
		}
		err = st.Inc(swarm.NewAddress(addr))
		if err != nil {
			t.Fatal(err)
		}
	}
	return st
}
