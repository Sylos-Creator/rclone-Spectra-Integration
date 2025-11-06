// Package spectra provides an interface to the Spectra synthetic filesystem
//
// Spectra is a synthetic filesystem backend for testing rclone operations.
// It generates deterministic, procedural directory structures and files for
// benchmarking, testing traversals, and validating data migration pipelines.
//
// See the backend documentation for more details:
// https://rclone.org/spectra/
package spectra

import (
	"context"
	"fmt"
	"io"
	iofs "io/fs"
	"path"
	"strings"
	"time"

	"github.com/Project-Sylos/Spectra/sdk"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/config/configmap"
	"github.com/rclone/rclone/fs/config/configstruct"
	"github.com/rclone/rclone/fs/hash"
)

// Register with Fs
func init() {
	fs.Register(&fs.RegInfo{
		Name:        "spectra",
		Description: "Spectra synthetic filesystem for testing",
		NewFs:       NewFs,
		Options: []fs.Option{
			{
				Name:     "config_path",
				Help:     "Path to Spectra configuration file",
				Required: true,
			},
			{
				Name:    "world",
				Help:    "World/table name to use (primary, s1, s2, etc.)",
				Default: "primary",
			},
		},
	})
}

// Example Spectra configuration file (spectra-config.json):
//
//	{
//	  "seed": {
//	    "max_depth": 4,
//	    "min_folders": 1,
//	    "max_folders": 3,
//	    "min_files": 2,
//	    "max_files": 5,
//	    "seed": 42,
//	    "db_path": "./spectra.db",
//	    "file_binary_seed": 0
//	  },
//	  "api": {
//	    "host": "localhost",
//	    "port": 8086
//	  },
//	  "secondary_tables": {
//	    "s1": 0.7,
//	    "s2": 0.3
//	  }
//	}
//
// Usage:
//   rclone config create myspectra spectra config_path=/path/to/spectra-config.json world=primary
//   rclone ls myspectra:
//   rclone tree myspectra: --max-depth 3

// Options defines the configuration for this backend
type Options struct {
	ConfigPath string `config:"config_path"`
	World      string `config:"world"`
}

// Fs represents a Spectra filesystem
type Fs struct {
	name       string         // name of this remote
	root       string         // the path we are working on if any
	opt        Options        // parsed config options
	spectraSDK *sdk.SpectraFS // Spectra SDK instance
	spectraFS  iofs.FS        // Spectra fs.FS for the selected world
	features   *fs.Features   // optional features
}

// Name of the remote (as passed into NewFs)
func (f *Fs) Name() string {
	return f.name
}

// Root of the remote (as passed into NewFs)
func (f *Fs) Root() string {
	return f.root
}

// String converts this Fs to a string
func (f *Fs) String() string {
	return fmt.Sprintf("Spectra root '%s' world '%s'", f.root, f.opt.World)
}

// Precision of the ModTimes in this Fs
func (f *Fs) Precision() time.Duration {
	// Spectra uses time.Now() for timestamps, so we have nanosecond precision
	return time.Nanosecond
}

// Hashes returns the supported hash sets
func (f *Fs) Hashes() hash.Set {
	return hash.Set(hash.SHA256)
}

// Features returns the optional features of this Fs
func (f *Fs) Features() *fs.Features {
	return f.features
}

// parsePath parses a remote 'url'
func parsePath(pth string) string {
	return strings.Trim(pth, "/")
}

// toSpectraPath converts rclone path (where "" is root) to Spectra path (where "/" is root)
func (f *Fs) toSpectraPath(rclonePath string) string {
	if rclonePath == "" {
		return "/"
	}
	// Join with root if there is one
	fullPath := rclonePath
	if f.root != "" {
		fullPath = path.Join(f.root, rclonePath)
	}
	// Ensure leading slash
	if !strings.HasPrefix(fullPath, "/") {
		fullPath = "/" + fullPath
	}
	return fullPath
}

// fromSpectraPath converts Spectra path to rclone path relative to f.root
func (f *Fs) fromSpectraPath(spectraPath string) string {
	// Remove leading slash
	pth := strings.TrimPrefix(spectraPath, "/")

	// If we have a root, make path relative to it
	if f.root != "" {
		rootPrefix := f.root + "/"
		if strings.HasPrefix(pth, rootPrefix) {
			pth = strings.TrimPrefix(pth, rootPrefix)
		} else if pth == f.root {
			pth = ""
		}
	}

	return pth
}

// NewFs constructs an Fs from the path
func NewFs(ctx context.Context, name, root string, m configmap.Mapper) (fs.Fs, error) {
	// Parse config into Options struct
	opt := new(Options)
	err := configstruct.Set(m, opt)
	if err != nil {
		return nil, err
	}

	// Initialize Spectra SDK
	spectraSDK, err := sdk.New(opt.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize Spectra SDK: %w", err)
	}

	// Validate that the requested world exists
	cfg := spectraSDK.GetConfig()
	if opt.World != "primary" {
		// Check if it exists in secondary tables
		if _, ok := cfg.SecondaryTables[opt.World]; !ok {
			return nil, fmt.Errorf("world '%s' not found in Spectra config (available: primary, %v)",
				opt.World, getSecondaryTableNames(cfg))
		}
	}

	// Get fs.FS wrapper for the selected world
	spectraFS := spectraSDK.AsFS(opt.World)

	root = parsePath(root)
	f := &Fs{
		name:       name,
		root:       root,
		opt:        *opt,
		spectraSDK: spectraSDK,
		spectraFS:  spectraFS,
	}

	f.features = (&fs.Features{
		CanHaveEmptyDirectories: true,
		ReadMimeType:            false,
		WriteMimeType:           false,
	}).Fill(ctx, f)

	// Check if root points to a file
	if root != "" {
		// For this check, we want the full path including root
		spectraPath := "/" + root
		fs.Debugf(nil, "NewFs: Checking if root '%s' (spectraPath='%s') is a file in world '%s'", root, spectraPath, opt.World)

		// Trigger lazy generation for parent directory
		parentPath := path.Dir(spectraPath)
		if parentPath == "." {
			parentPath = "/"
		}

		result, err := spectraSDK.ListChildren(&sdk.ListChildrenRequest{
			ParentPath: parentPath,
			TableName:  opt.World,
		})
		fs.Debugf(nil, "NewFs: ListChildren(parentPath='%s') result.Success=%v, err=%v", parentPath, result != nil && result.Success, err)

		// Now check if it's a file using SDK
		node, err := spectraSDK.GetNode(&sdk.GetNodeRequest{
			Path:      spectraPath,
			TableName: opt.World,
		})
		nodeType := ""
		if node != nil {
			nodeType = node.Type
		}
		fs.Debugf(nil, "NewFs: GetNode(path='%s') node=%v (type=%s), err=%v", spectraPath, node != nil, nodeType, err)
		if err == nil && node != nil {
			if node.Type != "folder" {
				fs.Debugf(nil, "NewFs: Root is a file, returning ErrorIsFile")
				// Root points to a file, adjust root to parent
				newRoot := path.Dir(root)
				if newRoot == "." {
					newRoot = ""
				}
				f.root = newRoot
				return f, fs.ErrorIsFile
			}
		}
	}

	return f, nil
}

// getSecondaryTableNames returns a list of secondary table names from config
func getSecondaryTableNames(cfg *sdk.Config) []string {
	names := make([]string, 0, len(cfg.SecondaryTables))
	for name := range cfg.SecondaryTables {
		names = append(names, name)
	}
	return names
}

// List the objects and directories in dir into entries
func (f *Fs) List(ctx context.Context, dir string) (entries fs.DirEntries, err error) {
	spectraPath := f.toSpectraPath(dir)
	// Remove leading slash for fs.FS (it expects relative paths)
	fsPath := strings.TrimPrefix(spectraPath, "/")
	if fsPath == "" {
		fsPath = "."
	}

	dirEntries, err := iofs.ReadDir(f.spectraFS, fsPath)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			return nil, fs.ErrorDirNotFound
		}
		return nil, err
	}

	for _, entry := range dirEntries {
		remote := entry.Name()
		if dir != "" {
			remote = path.Join(dir, entry.Name())
		}

		if entry.IsDir() {
			entries = append(entries, fs.NewDir(remote, time.Time{}))
		} else {
			// Get file info
			info, err := entry.Info()
			if err != nil {
				continue
			}

			obj := &Object{
				fs:      f,
				remote:  remote,
				size:    info.Size(),
				modTime: info.ModTime(),
			}
			entries = append(entries, obj)
		}
	}

	return entries, nil
}

// NewObject finds the Object at remote
func (f *Fs) NewObject(ctx context.Context, remote string) (fs.Object, error) {
	spectraPath := f.toSpectraPath(remote)

	// Trigger lazy generation by listing the parent directory
	parentPath := path.Dir(spectraPath)
	if parentPath == "." {
		parentPath = "/"
	}

	// List children to ensure lazy generation has occurred
	result, err := f.spectraSDK.ListChildren(&sdk.ListChildrenRequest{
		ParentPath: parentPath,
		TableName:  f.opt.World,
	})
	fs.Debugf(nil, "NewObject(%s): ListChildren result.Success=%v, err=%v", remote, result != nil && result.Success, err)

	// Now get the specific node
	node, err := f.spectraSDK.GetNode(&sdk.GetNodeRequest{
		Path:      spectraPath,
		TableName: f.opt.World,
	})
	fs.Debugf(nil, "NewObject(%s): GetNode node=%v, err=%v", remote, node != nil, err)

	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			return nil, fs.ErrorObjectNotFound
		}
		return nil, err
	}
	if node == nil {
		return nil, fs.ErrorObjectNotFound
	}

	if node.Type == "folder" {
		return nil, fs.ErrorIsDir
	}

	checksum := ""
	if node.Checksum != nil {
		checksum = *node.Checksum
	}

	return &Object{
		fs:       f,
		remote:   remote,
		size:     node.Size,
		modTime:  node.LastUpdated,
		checksum: checksum,
	}, nil
}

// Put uploads a new object
func (f *Fs) Put(ctx context.Context, in io.Reader, src fs.ObjectInfo, options ...fs.OpenOption) (fs.Object, error) {
	remote := src.Remote()
	spectraPath := f.toSpectraPath(remote)

	// Ensure parent directory exists
	parentPath := path.Dir(spectraPath)
	if parentPath != "/" && parentPath != "." {
		err := f.Mkdir(ctx, path.Dir(remote))
		if err != nil && err != fs.ErrorDirExists {
			return nil, fmt.Errorf("failed to create parent directory: %w", err)
		}
	}

	// Read the data
	data, err := io.ReadAll(in)
	if err != nil {
		return nil, fmt.Errorf("failed to read data: %w", err)
	}

	// Upload via SDK
	req := &sdk.UploadFileRequest{
		ParentPath: path.Dir(spectraPath),
		TableName:  f.opt.World,
		Name:       path.Base(remote),
		Data:       data,
	}

	node, err := f.spectraSDK.UploadFile(req)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	return &Object{
		fs:      f,
		remote:  remote,
		size:    node.Size,
		modTime: node.LastUpdated,
	}, nil
}

// Mkdir makes the directory
func (f *Fs) Mkdir(ctx context.Context, dir string) error {
	if dir == "" {
		return nil // root always exists
	}

	spectraPath := f.toSpectraPath(dir)

	// Check if it already exists
	fsPath := strings.TrimPrefix(spectraPath, "/")
	info, err := iofs.Stat(f.spectraFS, fsPath)
	if err == nil {
		if info.IsDir() {
			return nil // already exists
		}
		return fs.ErrorIsFile
	}

	// Create parent directories first
	parentPath := path.Dir(dir)
	if parentPath != "" && parentPath != "." {
		err := f.Mkdir(ctx, parentPath)
		if err != nil && err != fs.ErrorDirExists {
			return err
		}
	}

	// Create the directory
	req := &sdk.CreateFolderRequest{
		ParentPath: path.Dir(spectraPath),
		TableName:  f.opt.World,
		Name:       path.Base(dir),
	}

	_, err = f.spectraSDK.CreateFolder(req)
	if err != nil {
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("failed to create directory: %w", err)
	}

	return nil
}

// Rmdir removes the directory
func (f *Fs) Rmdir(ctx context.Context, dir string) error {
	if dir == "" {
		return fs.ErrorPermissionDenied
	}

	spectraPath := f.toSpectraPath(dir)

	// Check if directory exists and is empty
	fsPath := strings.TrimPrefix(spectraPath, "/")
	entries, err := iofs.ReadDir(f.spectraFS, fsPath)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			return fs.ErrorDirNotFound
		}
		return err
	}

	if len(entries) > 0 {
		return fs.ErrorDirectoryNotEmpty
	}

	// Delete the directory
	req := &sdk.DeleteNodeRequest{
		Path:      spectraPath,
		TableName: f.opt.World,
	}

	err = f.spectraSDK.DeleteNode(req)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "not found") {
			return fs.ErrorDirNotFound
		}
		return fmt.Errorf("failed to remove directory: %w", err)
	}

	return nil
}

// Check the interfaces are satisfied
var (
	_ fs.Fs = (*Fs)(nil)
)
