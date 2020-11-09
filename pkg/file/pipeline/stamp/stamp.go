// Copyright 2020 The Swarm Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package stamp

import (
	"crypto"

	"github.com/ethersphere/bee/pkg/file/pipeline"
	"github.com/ethersphere/bee/pkg/postage"
)

type stampWriter struct {
	stamper
	next pipeline.ChainWriter
}

// @zelig it is not really clear to me whether to propagate just 'Service' to the writer
/// which will then request the(or 'a') batch issuer from the service, or should i instantiate
// this writer directly with the issuer. the latter option is more limiting in the sense that
// we might want to pick an arbitrary issuer with free slots from Service, but then why do we need
// to provide the abstraction with the batch id at all in the first place? also, if we would like to
// move towards such an approach in the future, we would need to maybe rethink the Service abstraction
// and which kind of functionalities it provides. right now i will go with just the batch id and the service
// but yeah those points are worth addressing at least in general.
// another point is that the postage package has no interfaces whatsoever, forcing us to depend on far more
// concrete implementations when testing. for example in this case, i must use the concrete service, instead of just injecting
// a simple mock.
func NewStampWriter(signer crypto.Signer, ps *postage.Service, batch []byte, next pipeline.ChainWriter) pipeline.ChainWriter {

	return &stampWriter{next: next}
}

func (w *stampWriter) ChainWrite(p *pipeline.PipeWriteArgs) error {
	return w.next.ChainWrite(p)
}

func (w *stampWriter) Sum() ([]byte, error) {
	return w.next.Sum()
}
