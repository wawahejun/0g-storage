# 0G Storage Upload/Download Demo

This demo program demonstrates how to:
1. Split a 4GB file into 10 fragments (400MB each) 
2. Upload fragments to 0G Storage Network using the official Go API
3. Download fragments with integrity verification
4. Combine fragments into the final file

## Features

- **Fragment-based Upload**: Splits large files into configurable fragments for efficient upload
- **Indexer-based Upload**: Uses 0G Storage indexer for automatic node selection and upload
- **Merkle Tree Verification**: Verifies data integrity using Merkle roots
- **Configurable**: All parameters can be adjusted via JSON configuration
- **Context Timeout**: Proper timeout handling to prevent infinite waits
- **Complete Workflow**: Generate → Split → Upload → Download → Verify → Combine

## Prerequisites

- Go 1.19 or higher
- 0G Storage Network testnet/mainnet access
- Sufficient funds for storage transactions

## Installation

1. Navigate to the 0g-storage directory:
```bash
cd /home/wawahejun/0g/0g-storage
```

2. Install dependencies:
```bash
go mod tidy
```

3. Build the program:
```bash
go build upload_download_demo.go
```

## Configuration

Edit `config.demo.json` with your settings:

### Required Parameters

```json
{
  "blockchain": {
    "rpc_endpoint": "https://evmrpc-testnet.0g.ai/",
    "private_key": "YOUR_PRIVATE_KEY_HERE",
    "chain_id": 16600
  },
  "indexer": {
    "endpoint": "https://indexer-storage-testnet-turbo.0g.ai"
  }
}
```

**Important**: Replace `YOUR_PRIVATE_KEY_HERE` with your actual private key. The demo includes a testnet private key for demonstration.

### File Configuration

```json
{
  "file": {
    "input_file": "test_4gb_file.bin",
    "output_directory": "./output",
    "fragment_size": 209715200,
    "number_of_parts": 10,
    "generate_test_file": true,
    "test_file_size": 4294967296
  }
}
```

- `fragment_size`: 209715200 bytes = 200MB per fragment (current setting)
- `number_of_parts`: 10 fragments (10 × 200MB = ~2GB total)
- `generate_test_file`: Generate a test file if it doesn't exist
- `test_file_size`: 4294967296 bytes = 4GB test file size

### Upload Configuration

```json
{
  "upload": {
    "expected_replica": 1,
    "method": "min",
    "full_trusted": true,
    "max_retries": 5,
    "timeout_minutes": 60,
    "batch_size": 1
  }
}
```

- `method`: "min" for minimum replication, "splitable" for splitable upload
- `expected_replica`: Number of replicas to store (set to 1 for testnet)
- `full_trusted`: Use trusted nodes (set to true for faster uploads)
- `batch_size`: Number of fragments to process in parallel (currently 1 for stability)
- `timeout_minutes`: Timeout for each upload operation (60 minutes)
- `max_retries`: Maximum retry attempts (5 retries)

### Download Configuration

```json
{
  "download": {
    "verify_proof": true,
    "timeout_minutes": 60
  }
}
```

- `verify_proof`: Verify Merkle proofs during download (recommended)
- `timeout_minutes`: Timeout for download operations (60 minutes)

## Usage

### Run the Demo

```bash
./upload_download_demo -config config.demo.json
```

### Program Workflow

The demo executes the following steps automatically:

1. **Step 1: Generate Test File** (if enabled)
   - Creates a 4GB test file with sequential data (1MB chunks)
   - Progress displayed every 100MB
   - Can be disabled if you provide your own file

2. **Step 2: Split File**
   - Splits the 4GB file into 10 fragments (200MB each)
   - Saves fragments to `./output/parts/`
   - Creates part files: `part_00.bin`, `part_01.bin`, etc.

3. **Step 3: Upload Parts**
   - Uploads fragments one at a time (batch_size=1 for stability)
   - Uses indexer client for automatic node selection
   - Submits Merkle root to blockchain with TransactionPacked finality
   - Waits for transaction confirmation with timeout protection
   - Retries failed uploads up to 5 times

4. **Step 4: Download Parts**
   - Downloads all fragments using their root hashes
   - Verifies Merkle proofs (if enabled)
   - Saves to `./output/downloaded_parts/`

5. **Step 5: Verify Parts**
   - Calculates Merkle roots for original and downloaded fragments
   - Compares to ensure data integrity
   - Reports any mismatches

6. **Step 6: Combine Parts**
   - Combines all verified fragments into final file
   - Saves to `./output/final_file.bin`

### Example Output

```
Loading configuration from: config.demo.json
Initializing 0G Storage Demo...
✓ Demo initialized successfully

==================================================
STEP 1: Generating test file
==================================================
INFO[2024-01-01T00:00:00Z] Generating test file: test_4gb_file.bin (size: 4.00 GB)
INFO[2024-01-01T00:00:05Z] Progress: 100/4096 MB written
INFO[2024-01-01T00:00:10Z] Test file generation completed successfully
✓ Test file generated successfully

==================================================
STEP 2: Splitting file into parts
==================================================
INFO[2024-01-01T00:00:10Z] Splitting file: test_4gb_file.bin into 10 parts
INFO[2024-01-01T00:00:12Z] Created part 1: ./output/parts/part_00.bin (400.00 MB)
...
INFO[2024-01-01T00:00:30Z] Created part 10: ./output/parts/part_09.bin (400.00 MB)
✓ File split into 10 parts successfully

==================================================
STEP 3: Uploading parts
==================================================
INFO[2024-01-01T00:00:30Z] Starting upload of 10 parts with batch size 5
Fragment size: 419430400 bytes (400.00 MB)
Batch size: 5
Method: splitable
Expected replicas: 3

INFO[2024-01-01T00:00:30Z] Processing batch 1-5 of 10
INFO[2024-01-01T00:01:00Z] Uploading part 1: ./output/parts/part_00.bin
INFO[2024-01-01T00:03:00Z] ✓ Part 1 uploaded successfully - TxHash: 0x1234..., RootHash: 0xabcd...
...
✓ All parts uploaded successfully
Total transactions: 10

==================================================
STEP 4: Downloading parts
==================================================
...
✓ All parts downloaded successfully

==================================================
STEP 5: Verifying downloaded parts
==================================================
...
✓ All parts verified successfully

==================================================
STEP 6: Combining parts
==================================================
...
✓ Final file created successfully

==================================================
DEMO COMPLETED SUCCESSFULLY!
==================================================
Final file: ./output/final_file.bin
Total parts: 10
Total transactions: 10

Upload summary:
  Part 1: Tx=0x1234..., Root=0xabcd...
  ...
  Part 10: Tx=0x5678..., Root=0xefgh...
```

## Key Implementation Details

### Upload Method
- Uses `indexer.Upload()` method instead of direct node upload
- Implements `TransactionPacked` finality for faster confirmation
- Context timeout prevents infinite waits during log confirmation
- Individual file retry with exponential backoff

### Error Handling
- Proper timeout handling with context cancellation
- Retry mechanism for failed uploads
- Graceful error reporting with detailed logs

### Current Configuration Issues
- **Insufficient Funds**: The demo may fail with "insufficient funds for transfer" errors
- **Fragment Size**: Currently set to 200MB (was reduced from 400MB due to upload issues)
- **Batch Size**: Set to 1 for stability (was reduced from 5 due to timeout issues)

## Troubleshooting

### Common Issues

1. **Insufficient Funds Error**
   - Ensure your wallet has sufficient testnet tokens
   - Check transaction fees and gas prices
   - Consider reducing fragment size or number of parts

2. **Timeout Errors**
   - Increase `timeout_minutes` in configuration
   - Reduce `batch_size` to 1 for more stable uploads
   - Use `TransactionPacked` finality instead of `FileFinalized`

3. **Upload Failures**
   - Check network connectivity to testnet
   - Verify indexer endpoint is accessible
   - Ensure private key has proper permissions

**Note**: The demo currently uses a testnet configuration with reduced parameters for stability. For production use, adjust the configuration based on your specific requirements and network conditions.
