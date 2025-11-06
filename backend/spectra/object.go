// Object operations for Spectra backend
package spectra

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Project-Sylos/Spectra/sdk"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
)

// Object describes a Spectra file
type Object struct {
	fs       *Fs       // parent filesystem
	remote   string    // remote path
	size     int64     // file size
	modTime  time.Time // modification time
	checksum string    // cached checksum
}

// Fs returns the parent Fs
func (o *Object) Fs() fs.Info {
	return o.fs
}

// Remote returns the remote path
func (o *Object) Remote() string {
	return o.remote
}

// String returns a description of the Object
func (o *Object) String() string {
	if o == nil {
		return "<nil>"
	}
	return o.remote
}

// ModTime returns the modification time
func (o *Object) ModTime(ctx context.Context) time.Time {
	return o.modTime
}

// Size returns the size of the object
func (o *Object) Size() int64 {
	return o.size
}

// Hash returns the hash of the object
func (o *Object) Hash(ctx context.Context, ty hash.Type) (string, error) {
	if ty != hash.SHA256 {
		return "", hash.ErrUnsupported
	}

	// If we have cached checksum, return it
	if o.checksum != "" {
		return o.checksum, nil
	}

	// Get the node to fetch the checksum
	spectraPath := o.fs.toSpectraPath(o.remote)
	node, err := o.fs.spectraSDK.GetNode(&sdk.GetNodeRequest{
		Path:      spectraPath,
		TableName: o.fs.opt.World,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get node for hash: %w", err)
	}

	if node.Checksum != nil {
		o.checksum = *node.Checksum
	}
	return o.checksum, nil
}

// Storable returns whether the object is storable
func (o *Object) Storable() bool {
	return true
}

// SetModTime sets the modification time
func (o *Object) SetModTime(ctx context.Context, t time.Time) error {
	// Spectra doesn't support setting modification time
	return fs.ErrorCantSetModTime
}

// Open opens the file for read
func (o *Object) Open(ctx context.Context, options ...fs.OpenOption) (io.ReadCloser, error) {
	spectraPath := o.fs.toSpectraPath(o.remote)

	// Get the node first to ensure it exists and trigger lazy generation
	node, err := o.fs.spectraSDK.GetNode(&sdk.GetNodeRequest{
		Path:      spectraPath,
		TableName: o.fs.opt.World,
	})
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			return nil, fs.ErrorObjectNotFound
		}
		return nil, fmt.Errorf("failed to get node: %w", err)
	}

	// Get file data using SDK
	data, _, err := o.fs.spectraSDK.GetFileData(node.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to read file data: %w", err)
	}

	// Apply range options if specified
	var start, end int64 = 0, int64(len(data))
	for _, opt := range options {
		switch v := opt.(type) {
		case *fs.RangeOption:
			start, end = v.Decode(int64(len(data)))
		case *fs.SeekOption:
			start = v.Offset
		}
	}

	if start > 0 || end < int64(len(data)) {
		if start < 0 {
			start = 0
		}
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		data = data[start:end]
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

// Update updates the object with new content
func (o *Object) Update(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) error {
	// Read the new data
	data, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("failed to read data: %w", err)
	}

	spectraPath := o.fs.toSpectraPath(o.remote)

	// Delete the old file
	deleteReq := &sdk.DeleteNodeRequest{
		Path:      spectraPath,
		TableName: o.fs.opt.World,
	}
	err = o.fs.spectraSDK.DeleteNode(deleteReq)
	if err != nil {
		return fmt.Errorf("failed to delete old file: %w", err)
	}

	// Upload the new file
	uploadReq := &sdk.UploadFileRequest{
		ParentPath: o.fs.toSpectraPath(""),
		TableName:  o.fs.opt.World,
		Name:       o.remote,
		Data:       data,
	}

	node, err := o.fs.spectraSDK.UploadFile(uploadReq)
	if err != nil {
		return fmt.Errorf("failed to upload updated file: %w", err)
	}

	// Update object metadata
	o.size = node.Size
	o.modTime = node.LastUpdated
	o.checksum = "" // clear cached checksum

	return nil
}

// Remove removes the object
func (o *Object) Remove(ctx context.Context) error {
	spectraPath := o.fs.toSpectraPath(o.remote)

	req := &sdk.DeleteNodeRequest{
		Path:      spectraPath,
		TableName: o.fs.opt.World,
	}

	err := o.fs.spectraSDK.DeleteNode(req)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			return fs.ErrorObjectNotFound
		}
		return fmt.Errorf("failed to remove object: %w", err)
	}

	return nil
}

// Check the interfaces are satisfied
var (
	_ fs.Object = (*Object)(nil)
)
