package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/0gfoundation/0g-storage-client/common/blockchain"
	"github.com/0gfoundation/0g-storage-client/core"
	"github.com/0gfoundation/0g-storage-client/indexer"
	"github.com/0gfoundation/0g-storage-client/transfer"
	ethcommon "github.com/ethereum/go-ethereum/common"
	providers "github.com/openweb3/go-rpc-provider/provider_wrapper"
	web3go "github.com/openweb3/web3go"
	"github.com/sirupsen/logrus"
)

// Config represents the configuration for the demo
type Config struct {
	Blockchain struct {
		RPCEndpoint string `json:"rpc_endpoint"`
		PrivateKey  string `json:"private_key"`
		ChainID     int64  `json:"chain_id"`
	} `json:"blockchain"`
	Indexer struct {
		Endpoint string `json:"endpoint"`
	} `json:"indexer"`
	File struct {
		InputFile         string `json:"input_file"`
		OutputDirectory   string `json:"output_directory"`
		FragmentSize      int64  `json:"fragment_size"`
		NumberOfParts     int    `json:"number_of_parts"`
		GenerateTestFile  bool   `json:"generate_test_file"`
		TestFileSize      int64  `json:"test_file_size"`
	} `json:"file"`
	Upload struct {
		ExpectedReplica int    `json:"expected_replica"`
		Method          string `json:"method"`
		FullTrusted     bool   `json:"full_trusted"`
		MaxRetries      int    `json:"max_retries"`
		TimeoutMinutes  int    `json:"timeout_minutes"`
		BatchSize       int    `json:"batch_size"`
	} `json:"upload"`
	Download struct {
		VerifyProof    bool `json:"verify_proof"`
		TimeoutMinutes int  `json:"timeout_minutes"`
	} `json:"download"`
}

// Demo represents the upload/download demonstration
type Demo struct {
	config        *Config
	w3Client      *web3go.Client
	indexerClient *indexer.Client
	logger        *logrus.Logger
}

// NewDemo creates a new demo instance
func NewDemo(config *Config) (*Demo, error) {
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	// Initialize blockchain client
	w3Client, err := web3go.NewClientWithOption(
		config.Blockchain.RPCEndpoint,
		web3go.ClientOption{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create web3 client: %w", err)
	}

	// Initialize indexer client
	indexerClient, err := indexer.NewClient(config.Indexer.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create indexer client: %w", err)
	}

	return &Demo{
		config:        config,
		w3Client:      w3Client,
		indexerClient: indexerClient,
		logger:        logger,
	}, nil
}

// GenerateTestFile generates a test file of specified size
func (d *Demo) GenerateTestFile(filename string, size int64) error {
	d.logger.Infof("Generating test file: %s (size: %.2f GB)", filename, float64(size)/(1024*1024*1024))

	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	chunkSize := int64(1024 * 1024) // 1MB chunks
	chunk := make([]byte, chunkSize)
	for i := range chunk {
		chunk[i] = byte(i % 256)
	}

	totalChunks := size / chunkSize
	for i := int64(0); i < totalChunks; i++ {
		if _, err := file.Write(chunk); err != nil {
			return fmt.Errorf("failed to write chunk %d: %w", i, err)
		}
		if i%100 == 0 {
			d.logger.Infof("Progress: %d/%d MB written", i, totalChunks)
		}
	}

	d.logger.Info("Test file generation completed successfully")
	return nil
}

// SplitFile splits a large file into multiple parts
func (d *Demo) SplitFile(inputFile string, outputDir string) ([]string, error) {
	d.logger.Infof("Splitting file: %s into %d parts", inputFile, d.config.File.NumberOfParts)

	input, err := os.Open(inputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer input.Close()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	var partFiles []string
	buffer := make([]byte, d.config.File.FragmentSize)

	for i := 0; i < d.config.File.NumberOfParts; i++ {
		partFilename := filepath.Join(outputDir, fmt.Sprintf("part_%02d.bin", i))

		n, err := input.Read(buffer)
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("failed to read part %d: %w", i, err)
		}

		if n == 0 {
			break
		}

		partFile, err := os.Create(partFilename)
		if err != nil {
			return nil, fmt.Errorf("failed to create part file %d: %w", i, err)
		}

		if _, err := partFile.Write(buffer[:n]); err != nil {
			partFile.Close()
			return nil, fmt.Errorf("failed to write part %d: %w", i, err)
		}

		partFile.Close()
		partFiles = append(partFiles, partFilename)

		d.logger.Infof("Created part %d: %s (%.2f MB)", i+1, partFilename, float64(n)/(1024*1024))

		if err == io.EOF {
			break
		}
	}

	return partFiles, nil
}

// UploadParts uploads file parts in batches with enhanced error handling
func (d *Demo) UploadParts(partFiles []string) ([]ethcommon.Hash, []ethcommon.Hash, error) {
	d.logger.Infof("Starting upload of %d parts with batch size %d", len(partFiles), d.config.Upload.BatchSize)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(d.config.Upload.TimeoutMinutes)*time.Minute)
	defer cancel()

	var txHashes []ethcommon.Hash
	var rootHashes []ethcommon.Hash

	// Upload in batches with enhanced error handling
	batchSize := d.config.Upload.BatchSize
	for start := 0; start < len(partFiles); start += batchSize {
		end := start + batchSize
		if end > len(partFiles) {
			end = len(partFiles)
		}

		batch := partFiles[start:end]
		d.logger.Infof("Processing batch %d-%d of %d", start+1, end, len(partFiles))

		for _, partFile := range batch {
			// Retry individual file upload with exponential backoff
			var lastErr error
			for retry := 0; retry < d.config.Upload.MaxRetries; retry++ {
				if retry > 0 {
					d.logger.Infof("Retrying upload of %s (attempt %d/%d)", partFile, retry+1, d.config.Upload.MaxRetries)
					// Exponential backoff: 2^retry seconds
					backoff := time.Duration(math.Pow(2, float64(retry-1))) * time.Second
					if backoff > 30*time.Second {
						backoff = 30 * time.Second // Cap at 30 seconds
					}
					time.Sleep(backoff)
				}

				if err := d.uploadSingleFile(ctx, partFile, &txHashes, &rootHashes); err != nil {
					lastErr = err
					d.logger.Warnf("Upload attempt %d failed for %s: %v", retry+1, partFile, err)
					continue
				}
				// Success, break out of retry loop
				lastErr = nil
				break
			}

			if lastErr != nil {
				return nil, nil, fmt.Errorf("upload failed for %s after %d retries: %w", partFile, d.config.Upload.MaxRetries, lastErr)
			}
		}

		// Wait longer between batches to avoid overwhelming the network
		if end < len(partFiles) {
			d.logger.Info("Waiting before next batch...")
			time.Sleep(5 * time.Second)
		}
	}

	return txHashes, rootHashes, nil
}

// uploadSingleFile uploads a single file part using indexer node selection
func (d *Demo) uploadSingleFile(ctx context.Context, partFile string, txHashes *[]ethcommon.Hash, rootHashes *[]ethcommon.Hash) error {
	partIndex := len(*txHashes)
	d.logger.Infof("Uploading part %d: %s", partIndex+1, partFile)

	// Get root hash for verification first
	rootHash, err := core.MerkleRoot(partFile)
	if err != nil {
		return fmt.Errorf("failed to get merkle root: %w", err)
	}

	// Open the file part
	data, err := core.Open(partFile)
	if err != nil {
		return fmt.Errorf("failed to open part file: %w", err)
	}
	defer data.Close()

	// Create blockchain client with proper configuration
	w3Client := blockchain.MustNewWeb3(d.config.Blockchain.RPCEndpoint, d.config.Blockchain.PrivateKey, providers.Option{})
	defer w3Client.Close()

	// Create a context with timeout for this upload attempt
	uploadCtx, cancel := context.WithTimeout(ctx, time.Duration(d.config.Upload.TimeoutMinutes)*time.Minute)
	defer cancel()

	// Upload options with indexer-based node selection - use faster finality to avoid timeout issues
	uploadOpt := transfer.UploadOption{
		FinalityRequired: transfer.TransactionPacked, // Use TransactionPacked instead of FileFinalized for faster completion
		ExpectedReplica:  uint(d.config.Upload.ExpectedReplica),
		Method:           d.config.Upload.Method,
		FullTrusted:      d.config.Upload.FullTrusted,
		NRetries:         d.config.Upload.MaxRetries,
	}

	// Use indexer client for automatic node selection and upload
	txHash, err := d.indexerClient.Upload(uploadCtx, w3Client, data, uploadOpt)
	if err != nil {
		return fmt.Errorf("upload via indexer failed: %w", err)
	}

	*txHashes = append(*txHashes, txHash)
	*rootHashes = append(*rootHashes, rootHash)

	d.logger.Infof("✓ Part %d uploaded successfully via indexer - TxHash: %s, RootHash: %s",
		partIndex+1, txHash.Hex(), rootHash.Hex())

	return nil
}

// DownloadParts downloads all file parts
func (d *Demo) DownloadParts(rootHashes []ethcommon.Hash, outputDir string) error {
	d.logger.Infof("Starting download of %d parts", len(rootHashes))

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(d.config.Download.TimeoutMinutes)*time.Minute)
	defer cancel()

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create download directory: %w", err)
	}

	for i, rootHash := range rootHashes {
		outputFile := filepath.Join(outputDir, fmt.Sprintf("downloaded_part_%02d.bin", i))

		d.logger.Infof("Downloading part %d/%d: %s", i+1, len(rootHashes), rootHash.Hex())

		// Download using indexer client
		err := d.indexerClient.Download(ctx, rootHash.Hex(), outputFile, d.config.Download.VerifyProof)
		if err != nil {
			return fmt.Errorf("download failed for part %d: %w", i+1, err)
		}

		d.logger.Infof("✓ Part %d downloaded successfully to %s", i+1, outputFile)
	}

	return nil
}

// VerifyParts verifies the integrity of downloaded parts
func (d *Demo) VerifyParts(originalParts []string, downloadedDir string) error {
	d.logger.Info("Verifying integrity of downloaded parts")

	for i, originalPart := range originalParts {
		downloadedPart := filepath.Join(downloadedDir, fmt.Sprintf("downloaded_part_%02d.bin", i))

		// Get merkle root of original
		originalHash, err := core.MerkleRoot(originalPart)
		if err != nil {
			return fmt.Errorf("failed to get merkle root for original part %d: %w", i+1, err)
		}

		// Get merkle root of downloaded
		downloadedHash, err := core.MerkleRoot(downloadedPart)
		if err != nil {
			return fmt.Errorf("failed to get merkle root for downloaded part %d: %w", i+1, err)
		}

		// Compare
		if originalHash != downloadedHash {
			return fmt.Errorf("hash mismatch for part %d: expected %s, got %s",
				i+1, originalHash.Hex(), downloadedHash.Hex())
		}

		d.logger.Infof("✓ Part %d verification passed: %s", i+1, originalHash.Hex())
	}

	d.logger.Info("All parts verified successfully")
	return nil
}

// CombineParts combines all downloaded parts into a single file
func (d *Demo) CombineParts(downloadedDir string, outputFile string) error {
	d.logger.Infof("Combining %d parts into: %s", d.config.File.NumberOfParts, outputFile)

	output, err := os.Create(outputFile)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer output.Close()

	for i := 0; i < d.config.File.NumberOfParts; i++ {
		partFile := filepath.Join(downloadedDir, fmt.Sprintf("downloaded_part_%02d.bin", i))

		input, err := os.Open(partFile)
		if err != nil {
			return fmt.Errorf("failed to open part file %d: %w", i+1, err)
		}

		written, err := io.Copy(output, input)
		if err != nil {
			input.Close()
			return fmt.Errorf("failed to copy part %d: %w", i+1, err)
		}

		input.Close()
		d.logger.Infof("✓ Combined part %d (%d bytes)", i+1, written)
	}

	d.logger.Infof("All parts combined successfully. Final file: %s", outputFile)
	return nil
}

// LoadConfig loads configuration from a JSON file
func LoadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var config Config
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

func main() {
	// Command line flags
	configFile := flag.String("config", "config.demo.json", "Path to configuration file")
	flag.Parse()

	// Load configuration
	fmt.Println("Loading configuration from:", *configFile)
	config, err := LoadConfig(*configFile)
	if err != nil {
		fmt.Printf("❌ Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Create demo instance
	fmt.Println("Initializing 0G Storage Demo...")
	demo, err := NewDemo(config)
	if err != nil {
		fmt.Printf("❌ Failed to initialize demo: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("✓ Demo initialized successfully")
	fmt.Println()

	// Step 1: Generate test file (if enabled)
	if config.File.GenerateTestFile {
		fmt.Println("=" + string(make([]byte, 50)))
		fmt.Println("STEP 1: Generating test file")
		fmt.Println("=" + string(make([]byte, 50)))
		if err := demo.GenerateTestFile(config.File.InputFile, config.File.TestFileSize); err != nil {
			fmt.Printf("❌ Failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("✓ Test file generated successfully")
		fmt.Println()
	}

	// Step 2: Split file
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Println("STEP 2: Splitting file into parts")
	fmt.Println("=" + string(make([]byte, 50)))
	partsDir := filepath.Join(config.File.OutputDirectory, "parts")
	partFiles, err := demo.SplitFile(config.File.InputFile, partsDir)
	if err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ File split into %d parts successfully\n", len(partFiles))
	fmt.Println()

	// Step 3: Upload parts
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Printf("STEP 3: Uploading %d parts\n", len(partFiles))
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Printf("Fragment size: %d bytes (%.2f MB)\n", config.File.FragmentSize, float64(config.File.FragmentSize)/(1024*1024))
	fmt.Printf("Batch size: %d\n", config.Upload.BatchSize)
	fmt.Printf("Method: %s\n", config.Upload.Method)
	fmt.Printf("Expected replicas: %d\n", config.Upload.ExpectedReplica)
	fmt.Println()
	txHashes, rootHashes, err := demo.UploadParts(partFiles)
	if err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("✓ All parts uploaded successfully")
	fmt.Printf("Total transactions: %d\n", len(txHashes))
	fmt.Println()

	// Step 4: Download parts
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Println("STEP 4: Downloading parts")
	fmt.Println("=" + string(make([]byte, 50)))
	downloadDir := filepath.Join(config.File.OutputDirectory, "downloaded_parts")
	if err := demo.DownloadParts(rootHashes, downloadDir); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ All parts downloaded successfully")
	fmt.Println()

	// Step 5: Verify parts
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Println("STEP 5: Verifying downloaded parts")
	fmt.Println("=" + string(make([]byte, 50)))
	if err := demo.VerifyParts(partFiles, downloadDir); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Println("✓ All parts verified successfully")
	fmt.Println()

	// Step 6: Combine parts
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Println("STEP 6: Combining parts")
	fmt.Println("=" + string(make([]byte, 50)))
	finalFile := filepath.Join(config.File.OutputDirectory, "final_file.bin")
	if err := demo.CombineParts(downloadDir, finalFile); err != nil {
		fmt.Printf("❌ Failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Final file created successfully")
	fmt.Println()

	// Final summary
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Println("DEMO COMPLETED SUCCESSFULLY!")
	fmt.Println("=" + string(make([]byte, 50)))
	fmt.Printf("Final file: %s\n", finalFile)
	fmt.Printf("Total parts: %d\n", len(partFiles))
	fmt.Printf("Total transactions: %d\n", len(txHashes))
	fmt.Println()
	fmt.Println("Upload summary:")
	for i, txHash := range txHashes {
		fmt.Printf("  Part %d: Tx=%s, Root=%s\n", i+1, txHash.Hex()[:10]+"...", rootHashes[i].Hex()[:10]+"...")
	}
}
