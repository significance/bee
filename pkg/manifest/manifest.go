// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package manifest

import (
	"errors"

	"github.com/ethersphere/bee/pkg/file"
	"github.com/ethersphere/bee/pkg/swarm"
)

const DefaultManifestType = ManifestMantarayContentType

var (
	// ErrNotFound is returned when an Entry is not found in the manifest.
	ErrNotFound = errors.New("manifest: not found")

	// ErrInvalidManifestType is returned when an unknown manifest type
	// is provided to the function.
	ErrInvalidManifestType = errors.New("manifest: invalid type")
)

// Interface for operations with manifest.
type Interface interface {
	// Type returns manifest implementation type information
	Type() string
	// Add a manifest entry to the specified path.
	Add(string, Entry) error
	// Remove a manifest entry on the specified path.
	Remove(string) error
	// Lookup returns a manifest entry if one is found in the specified path.
	Lookup(string) (Entry, error)
	// HasPrefix tests whether the specified prefix path exists.
	HasPrefix(string) (bool, error)
	// Store stores the manifest, returning the resulting address.
	Store() (swarm.Address, error)
}

// Entry represents a single manifest entry.
type Entry interface {
	// Reference returns the address of the file.
	Reference() swarm.Address
	// Metadata returns the metadata of the file.
	Metadata() map[string]string
}

// NewDefaultManifest creates a new manifest with default type.
func NewDefaultManifest(ls file.LoadSaver) (Interface, error) {
	return NewManifest(DefaultManifestType, ls)
}

// NewManifest creates a new manifest.
func NewManifest(
	manifestType string,
	ls file.LoadSaver,
) (Interface, error) {
	switch manifestType {
	case ManifestSimpleContentType:
		return NewSimpleManifest(ls)
	case ManifestMantarayContentType:
		return NewMantarayManifest(ls)
	default:
		return nil, ErrInvalidManifestType
	}
}

// NewManifestReference loads existing manifest.
func NewManifestReference(
	manifestType string,
	reference swarm.Address,
	l file.LoadSaver,
) (Interface, error) {
	switch manifestType {
	case ManifestSimpleContentType:
		return NewSimpleManifestReference(reference, l)
	case ManifestMantarayContentType:
		return NewMantarayManifestReference(reference, l)
	default:
		return nil, ErrInvalidManifestType
	}
}

type manifestEntry struct {
	reference swarm.Address
	metadata  map[string]string
}

// NewEntry creates a new manifest entry.
func NewEntry(reference swarm.Address, metadata map[string]string) Entry {
	return &manifestEntry{
		reference: reference,
		metadata:  metadata,
	}
}

func (e *manifestEntry) Reference() swarm.Address {
	return e.reference
}

func (e *manifestEntry) Metadata() map[string]string {
	return e.metadata
}
