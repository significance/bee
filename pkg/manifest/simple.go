// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package manifest

import (
	"errors"
	"fmt"

	"github.com/ethersphere/bee/pkg/file"
	"github.com/ethersphere/bee/pkg/swarm"
	"github.com/ethersphere/manifest/simple"
)

const (
	// ManifestSimpleContentType represents content type used for noting that
	// specific file should be processed as 'simple' manifest
	ManifestSimpleContentType = "application/bzz-manifest-simple+json"
)

type simpleManifest struct {
	manifest simple.Manifest

	ls file.LoadSaver
}

// NewSimpleManifest creates a new simple manifest.
func NewSimpleManifest(
	ls file.LoadSaver,
) (Interface, error) {
	return &simpleManifest{
		manifest: simple.NewManifest(),
		ls:       ls,
	}, nil
}

// NewSimpleManifestReference loads existing simple manifest.
func NewSimpleManifestReference(ref swarm.Address, l file.LoadSaver) (Interface, error) {
	m := &simpleManifest{
		manifest: simple.NewManifest(),
		ls:       l,
	}
	err := m.load(ref)
	return m, err
}

func (m *simpleManifest) Type() string {
	return ManifestSimpleContentType
}

func (m *simpleManifest) Add(path string, entry Entry) error {
	e := entry.Reference().String()

	return m.manifest.Add(path, e, entry.Metadata())
}

func (m *simpleManifest) Remove(path string) error {
	err := m.manifest.Remove(path)
	if err != nil {
		if errors.Is(err, simple.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}

	return nil
}

func (m *simpleManifest) Lookup(path string) (Entry, error) {
	n, err := m.manifest.Lookup(path)
	if err != nil {
		return nil, ErrNotFound
	}

	address, err := swarm.ParseHexAddress(n.Reference())
	if err != nil {
		return nil, fmt.Errorf("parse swarm address: %w", err)
	}

	entry := NewEntry(address, n.Metadata())

	return entry, nil
}

func (m *simpleManifest) HasPrefix(prefix string) (bool, error) {
	return m.manifest.HasPrefix(prefix), nil
}

func (m *simpleManifest) Store() (swarm.Address, error) {
	data, err := m.manifest.MarshalBinary()
	if err != nil {
		return swarm.ZeroAddress, fmt.Errorf("manifest marshal error: %w", err)
	}

	ref, err := m.ls.Save(data)
	if err != nil {
		return swarm.ZeroAddress, fmt.Errorf("manifest save error: %w", err)
	}

	return swarm.NewAddress(ref), nil
}

func (m *simpleManifest) load(reference swarm.Address) error {
	buf, err := m.ls.Load(reference.Bytes())
	if err != nil {
		return fmt.Errorf("manifest load error: %w", err)
	}

	err = m.manifest.UnmarshalBinary(buf)
	if err != nil {
		return fmt.Errorf("manifest unmarshal error: %w", err)
	}

	return nil
}
