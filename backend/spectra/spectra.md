# Spectra

Spectra is a synthetic filesystem backend for testing rclone operations. It generates deterministic, procedural directory structures and files for benchmarking, testing traversals, and validating data migration pipelines.

## Features

* **Procedural Generation**: Creates folder and file hierarchies based on configurable parameters
* **Deterministic Mode**: Same seed produces identical filesystem structures across runs
* **Multiple Worlds**: Test with different data distributions across parallel "worlds" (primary, s1, s2, etc.)
* **Lazy Generation**: Files and folders generated on-demand when accessed
* **SHA-256 Checksums**: All files include deterministic checksums for integrity validation
* **DuckDB Backend**: Persistent storage with reproducible state
* **No Network I/O**: Pure local filesystem simulator - perfect for offline testing

## Configuration

Spectra requires a JSON configuration file. Here is an example `spectra-config.json`:

```json
{
  "seed": {
    "max_depth": 4,
    "min_folders": 1,
    "max_folders": 3,
    "min_files": 2,
    "max_files": 5,
    "seed": 42,
    "db_path": "./spectra.db",
    "file_binary_seed": 0
  },
  "api": {
    "host": "localhost",
    "port": 8086
  },
  "secondary_tables": {
    "s1": 0.7,
    "s2": 0.3
  }
}
```

### Configuration Parameters

#### Seed Configuration

* `max_depth` - Maximum depth of folder hierarchy (minimum 1)
* `min_folders` - Minimum number of subfolders per directory
* `max_folders` - Maximum number of subfolders per directory
* `min_files` - Minimum number of files per directory
* `max_files` - Maximum number of files per directory
* `seed` - Random seed for deterministic generation
* `db_path` - Path to DuckDB database file
* `file_binary_seed` - Seed for deterministic file data generation (default: 0)

#### Secondary Tables (Worlds)

The `secondary_tables` map defines additional "worlds" with probability of node existence:

* Key: World name (e.g., "s1", "s2")
* Value: Probability (0.0-1.0) that each node exists in this world

This enables testing migration scenarios where source and destination have different file sets.

## Usage

### Interactive Configuration

Configure a Spectra remote:

```
rclone config create myspectra spectra config_path=/path/to/spectra-config.json world=primary
```

### Command Line

You can also use Spectra directly on the command line:

```
rclone ls :spectra,config_path=/path/to/spectra-config.json,world=primary:
```

### Standard Options

Spectra supports the following standard rclone options:

* `--checksum` - Validate SHA-256 checksums
* `--dry-run` - Show what would be copied without actually copying
* `-vv` - Verbose output for debugging

## Examples

### List Files

List all files in the primary world:

```
rclone ls myspectra:
```

### Show Directory Tree

Display the folder structure:

```
rclone tree myspectra: --max-depth 3
```

### Copy Files

Copy files from Spectra to local filesystem:

```
rclone copy myspectra:folder1 /tmp/test-output
```

### Verify Checksums

Check file integrity:

```
rclone hashsum sha256 myspectra:
```

### Compare Worlds

Test migration scenarios by comparing different worlds:

```
# Configure two remotes pointing to different worlds
rclone config create spectra-src spectra config_path=/path/to/config.json world=primary
rclone config create spectra-dst spectra config_path=/path/to/config.json world=s1

# Compare the worlds
rclone check spectra-src: spectra-dst: --combined -
```

### Dry Run Testing

Test sync operations without actually transferring data:

```
rclone sync spectra-src: spectra-dst: --dry-run -vv
```

## Use Cases

### Migration Pipeline Testing

Test your migration logic with reproducible filesystem structures:

```bash
# Generate test filesystem
rclone config create test-source spectra config_path=/path/to/config.json world=primary

# Run your migration
rclone sync test-source: production-destination: --dry-run
```

### Performance Benchmarking

Measure traversal performance without network or disk I/O overhead:

```bash
time rclone ls myspectra: --fast-list
```

### Traversal Algorithm Validation

Verify your traversal logic handles various directory structures:

```bash
# Deep hierarchy
cat > deep-config.json <<EOF
{
  "seed": {
    "max_depth": 10,
    "min_folders": 1,
    "max_folders": 2,
    "min_files": 5,
    "max_files": 10,
    "seed": 12345,
    "db_path": "./test-deep.db",
    "file_binary_seed": 0
  }
}
EOF

rclone tree :spectra,config_path=./deep-config.json:
```

### Multi-Source Testing

Simulate scenarios with multiple data sources having different file sets:

```bash
# Primary has 100% of files
# s1 has ~70% of files (probability 0.7)
# s2 has ~30% of files (probability 0.3)

rclone config create world-primary spectra config_path=/path/to/config.json world=primary
rclone config create world-s1 spectra config_path=/path/to/config.json world=s1
rclone config create world-s2 spectra config_path=/path/to/config.json world=s2

# Compare what's unique to each world
rclone check world-primary: world-s1: --combined -
```

## Technical Details

### File Data

All files are 1KB (1024 bytes) in size with deterministic random content based on the `file_binary_seed` configuration parameter. The same file ID always produces the same bytes, ensuring consistent checksums across reads.

### Lazy Generation

Files and folders are generated on-demand when their parent directory is listed. This ensures fast initialization even for large, deep hierarchies.

### Checksums

Spectra provides SHA-256 checksums for all files. These checksums are deterministic and will match across multiple reads of the same file.

### World Filtering

Each node (file/folder) has an "existence map" that determines which worlds it appears in. When you access a specific world, Spectra filters nodes to only show those that exist in that world.

### Database Storage

Spectra uses DuckDB to persist the filesystem structure. Delete the database file to reset and regenerate a new filesystem:

```bash
rm ./spectra.db
rclone ls myspectra:  # Regenerates from scratch
```

## Limitations

* Files are always 1KB in size
* Modification times are set at generation time and cannot be changed
* No support for special files (symlinks, devices, etc.)
* Designed for testing only - not for production data storage

## Notes

* The same seed will always generate the same filesystem structure
* Spectra is read-only from rclone's perspective (write operations create new nodes but don't modify existing ones)
* Perfect for CI/CD testing pipelines where you need reproducible test data

## See Also

* [Spectra Library Documentation](https://github.com/Project-Sylos/Spectra)
* [Memory Backend](https://rclone.org/memory/) - Similar in-memory testing backend
* [Rclone Testing Guide](https://rclone.org/docs/)

