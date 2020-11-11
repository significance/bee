// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package manifest

import (
	"errors"
	"fmt"

	"github.com/ethersphere/bee/pkg/file"
	"github.com/ethersphere/bee/pkg/swarm"
	"github.com/ethersphere/manifest/mantaray"
)

const (
	// ManifestMantarayContentType represents content type used for noting that
	// specific file should be processed as mantaray manifest.
	ManifestMantarayContentType = "application/bzz-manifest-mantaray+octet-stream"
)

type mantarayManifest struct {
	trie *mantaray.Node

	ls file.LoadSaver
}

// NewMantarayManifest creates a new mantaray-based manifest.
func NewMantarayManifest(l file.LoadSaver) (Interface, error) {
	return &mantarayManifest{
		trie: mantaray.New(),
		ls:   l,
	}, nil
}

// NewMantarayManifestReference loads existing mantaray-based manifest.
func NewMantarayManifestReference(
	reference swarm.Address,
	ls file.LoadSaver,
) (Interface, error) {
	return &mantarayManifest{
		trie: mantaray.NewNodeRef(reference.Bytes()),
		ls:   ls,
	}, nil
}

func (m *mantarayManifest) Type() string {
	return ManifestMantarayContentType
}

func (m *mantarayManifest) Add(path string, entry Entry) error {
	p := []byte(path)
	e := entry.Reference().Bytes()

	return m.trie.Add(p, e, entry.Metadata(), m.ls)
}

func (m *mantarayManifest) Remove(path string) error {
	p := []byte(path)

	err := m.trie.Remove(p, m.ls)
	if err != nil {
		if errors.Is(err, mantaray.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	return nil
}

func (m *mantarayManifest) Lookup(path string) (Entry, error) {
	p := []byte(path)

	node, err := m.trie.LookupNode(p, m.ls)
	if err != nil {
		return nil, ErrNotFound
	}

	if !node.IsValueType() {
		return nil, ErrNotFound
	}

	address := swarm.NewAddress(node.Entry())

	entry := NewEntry(address, node.Metadata())

	return entry, nil
}

func (m *mantarayManifest) HasPrefix(prefix string) (bool, error) {
	p := []byte(prefix)

	return m.trie.HasPrefix(p, m.ls)
}

func (m *mantarayManifest) Store() (swarm.Address, error) {
	err := m.trie.Save(m.ls)
	if err != nil {
		return swarm.ZeroAddress, fmt.Errorf("manifest save error: %w", err)
	}

	address := swarm.NewAddress(m.trie.Reference())

	return address, nil
}
