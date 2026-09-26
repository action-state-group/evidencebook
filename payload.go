// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PayloadResolver turns a committed payload digest back into bytes. It is
// digest-addressed: the header commits digests, never storage locations. The
// semantic digest (what the header commits) and the storage digest (of the
// bytes as held at rest, after any host transform) are independent values.
type PayloadResolver interface {
	Put(ctx context.Context, semanticDigest string, data []byte) error
	// Resolve returns the bytes, or ErrNotFound when they are not held.
	// Retention policy is applied by the book, not the resolver.
	Resolve(ctx context.Context, semanticDigest string) ([]byte, error)
	StorageDigest(ctx context.Context, semanticDigest string) (string, error)
	Delete(ctx context.Context, semanticDigest string) error
}

// PayloadDir is the reference PayloadResolver.
type PayloadDir struct{ dir string }

// OpenPayloadDir stores payload bytes as files named by semantic digest, with
// no storage transform, so the storage digest equals the semantic digest.
func OpenPayloadDir(dir string) (*PayloadDir, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &PayloadDir{dir: dir}, nil
}

func (p *PayloadDir) path(digest string) (string, error) {
	if !hex64.MatchString(digest) {
		return "", fmt.Errorf("%w: payload digest %q", ErrInvalid, digest)
	}
	return filepath.Join(p.dir, digest), nil
}

func (p *PayloadDir) Put(_ context.Context, digest string, data []byte) error {
	if digestBytes(data) != digest {
		return fmt.Errorf("%w: payload bytes do not hash to %s", ErrInvalid, digest)
	}
	path, err := p.path(digest)
	if err != nil {
		return err
	}
	// Write, fsync, then rename, so a crash never leaves a torn file under
	// the digest's name.
	tmp, err := os.CreateTemp(p.dir, digest+".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if err := errors.Join(werr, tmp.Sync(), tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	dir, err := os.Open(p.dir)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func (p *PayloadDir) Resolve(_ context.Context, digest string) ([]byte, error) {
	path, err := p.path(digest)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: payload %s", ErrNotFound, digest)
	}
	if err != nil {
		return nil, err
	}
	if digestBytes(data) != digest {
		return nil, fmt.Errorf("%w: stored bytes for payload %s do not hash to it", ErrCorrupt, digest)
	}
	return data, nil
}

func (p *PayloadDir) StorageDigest(ctx context.Context, digest string) (string, error) {
	data, err := p.Resolve(ctx, digest)
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

func (p *PayloadDir) Delete(_ context.Context, digest string) error {
	path, err := p.path(digest)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
