package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sigstore/sigstore/pkg/tuf"
)

func main() {
	// Define default mirror URL and parse command-line flags
	defaultMirror := "https://tuf-repo-cdn.sigstore.dev"
	mirror := flag.String("mirror", defaultMirror, "Sigstore TUF Repository Mirror")
	help := flag.Bool("help", false, "Print this help message")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *help {
		flag.Usage()
		os.Exit(0)
	}
	log.SetFlags(0)
	log.SetOutput(os.Stderr)

	// Create a temporary repository directory to store tuf resources
	temporaryWorkingDirectory, err := os.MkdirTemp("", "tuf-repository-*")
	if err != nil {
		log.Fatalf("Error: could not create temporary directory: %v", err)
	}
	defer os.RemoveAll(temporaryWorkingDirectory)

	// Get the latest root.json file name from the mirror
	latestRootName, _ := GetLatestMetadataName(*mirror, "root.json")
	if latestRootName == "" {
		log.Fatalf("Error: could not get the latest root.json file from the mirror")
		os.Exit(1)
	}

	// Construct the URL for the root.json file
	rootURL := fmt.Sprintf("%s/%s", *mirror, latestRootName)
	log.Printf("mirror %s, root %s\n", *mirror, rootURL)
	rootJSONFile := &os.File{}

	// List of metadata files to download
	madatadas := []string{"root.json", "snapshot.json", "targets.json", "timestamp.json"}
	for _, metadata := range madatadas {
		metadataName := ""
		if metadata == "timestamp.json" {
			metadataName = "timestamp.json"
		} else {
			metadataName, _ = GetLatestMetadataName(*mirror, metadata)
		}
		metadataURL := fmt.Sprintf("%s/%s", *mirror, metadataName)
		metadataFilepath := filepath.Join(temporaryWorkingDirectory, metadataName)
		metadataFile, err := os.Create(metadataFilepath)
		if err != nil {
			log.Fatalf("Error: could not create file %s: %v", metadataFile.Name(), err)
		}
		err = DownloadFile(metadataFile, metadataURL)
		if err != nil {
			log.Fatalf("Error: could not download %s from %s", metadataFile.Name(), metadataURL)
		}
		if metadata == "root.json" {
			rootJSONFile = metadataFile
			if err != nil {
				log.Fatalf("Error: could not base64-encode root.json: %v", err)
			}
		}
	}

	// Delete the local TUF repository
	if err := cleanupLocalTUFRepository(); err != nil {
		log.Fatalf("Error: could not cleanup local TUF repository: %v", err)
	}

	// Initialize the local TUF repository
	ctx := context.Background()
	rootJSON, _ := os.ReadFile(rootJSONFile.Name())
	if err := tuf.Initialize(ctx, *mirror, rootJSON); err != nil {
		log.Fatalf("Error: could not initialize TUF: %v", err)
	}

	// Get and print the root status
	rootStatus, err := tuf.GetRootStatus(ctx)
	if err != nil {
		log.Fatalf("Error: could not get root status: %v", err)
	}
	rootStatusJSON, err := json.MarshalIndent(rootStatus, "", "  ")
	if err != nil {
		log.Fatalf("Error: could not marshal root status to JSON: %v", err)
	}
	log.Default().Printf("Root status: %s\n", rootStatusJSON)

	// Move the targets directory to the temporary working directory
	originalTargetsDir := filepath.Join(os.Getenv("HOME"), ".sigstore", "root", "targets")
	destinationTargetsDir := filepath.Join(temporaryWorkingDirectory, "targets")
	err = os.Rename(originalTargetsDir, destinationTargetsDir)
	if err != nil {
		log.Fatalf("Failed to move directory: %v", err)
	}

	// Compress the repository directory into a tar.gz file
	repositoryArchive, err := os.CreateTemp("", "repository-*.tar.gz")
	if err != nil {
		log.Fatalf("Error: could not create temporary file for repository archive: %v", err)
	}
	defer repositoryArchive.Close()
	err = CompressDirectory(temporaryWorkingDirectory, repositoryArchive.Name())
	if err != nil {
		log.Fatalf("Error: could not compress repository directory: %v", err)
	}

	// Base64 encode the repository archive file
	b64RepositoryArchive, err := EncodeBase64(repositoryArchive)
	if err != nil {
		log.Fatalf("Error: could not base64-encode repository archive: %v", err)
	}

	// Base64 encode the root.json file
	b64RootJSON, err := EncodeBase64(rootJSONFile)
	if err != nil {
		log.Fatalf("Error: could not base64-encode root.json: %v", err)
	}

	// Print the TrustRoot Custom Resource YAML to stdout
	trustRootYAML := fmt.Sprintf(`apiVersion: policy.sigstore.dev/v1alpha1
kind: TrustRoot
metadata:
  name: %s-%d
spec:
  repository:
    root: |-
      %s
    mirrorFS: |-
      %s
`, strings.ReplaceAll(*mirror, "https://", ""), time.Now().Unix(), b64RootJSON, b64RepositoryArchive)
	fmt.Println(trustRootYAML)
}

// cleanupLocalTUFRepository removes the local TUF (The Update Framework) repository
func cleanupLocalTUFRepository() error {
	tufDir := filepath.Join(os.Getenv("HOME"), ".sigstore")
	if err := os.RemoveAll(tufDir); err != nil {
		return fmt.Errorf("error: could not remove TUF directory %s: %v", tufDir, err)
	}
	return nil
}

// DownloadFile downloads file from the provided URL and saves it to the given file.
// Parameters:
//   - destinationFile: target file where downloaded content will be written
//   - url: source URL to download the file from
//
// Returns:
//   - error: nil if successful, otherwise error describing what went wrong
func DownloadFile(destinationFile *os.File, url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download file: %s", resp.Status)
	}
	defer resp.Body.Close()
	_, err = io.Copy(destinationFile, resp.Body)
	return err
}

// CompressDirectory compresses the contents of the specified source directory
// into a tar.gz archive at the specified destination path.
//
// Parameters:
//   - src: The path to the source directory to be compressed.
//   - dst: The path to the destination tar.gz file.
//
// Returns:
//   - error: nil if successful, otherwise an error describing what went wrong.
//
// The function performs the following steps:
//  1. Creates the output file at the destination path.
//  2. Creates a gzip writer and a tar writer.
//  3. Walks through the source directory, adding files and directories to the tar archive.
//  4. Writes file contents to the tar archive for regular files.
//
// Example usage:
//
//	err := CompressDirectory("/path/to/source", "/path/to/destination.tar.gz")
//	if err != nil {
//	    log.Fatalf("Error compressing directory: %v", err)
//	}
func CompressDirectory(src, dst string) error {
	// Create the output file
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	// Create gzip writer
	gw := gzip.NewWriter(out)
	defer gw.Close()
	// Create tar writer
	tw := tar.NewWriter(gw)
	defer tw.Close()
	// Walk through the source directory
	err = filepath.Walk(src, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Get the relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		// Skip the root directory itself
		if relPath == "." {
			return nil
		}
		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = relPath
		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		// If it's a regular file, write its contents
		if info.Mode().IsRegular() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()
			if _, err := io.Copy(tw, file); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// encodeBase64 reads the content of the provided file and encodes it in base64 format.
// It takes a pointer to an os.File as input and returns the base64 encoded string and an error, if any.
// Parameters:
//   - sourceFile: A pointer to the os.File to be encoded.
//
// Returns:
//   - A base64 encoded string representation of the file's content.
//   - An error if there is an issue reading the file.
func EncodeBase64(sourceFile *os.File) (string, error) {
	data, err := os.ReadFile(sourceFile.Name())
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// GetLatestMetadataName fetches the directory listing from the specified mirror URL,
// searches for files matching the given metadata pattern, and returns the name of the latest file.
//
// Parameters:
//   - mirror: The URL of the mirror to fetch the directory listing from.
//   - metadataPattern: The pattern to match metadata file names.
//
// Returns:
//   - The name of the latest metadata file matching the pattern.
//   - An error if the directory listing could not be fetched or no matching files were found.
func GetLatestMetadataName(mirror string, metadataPattern string) (string, error) {
	resp, err := http.Get(mirror)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch mirror directory: %s", resp.Status)
	}
	// Assuming the mirror returns a plain text listing of files
	var files []string
	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	// Parse the response body to extract file names
	// This is a simplified example; adjust parsing as needed
	re := regexp.MustCompile(fmt.Sprintf(`(\d+\.%s)`, regexp.QuoteMeta(metadataPattern)))
	for _, line := range strings.Split(string(body), "\n") {
		if matches := re.FindStringSubmatch(line); len(matches) > 0 {
			files = append(files, matches[1])
		}
	}
	// log.Default().Printf("Metadata files found in mirror directory: %v\n", files)
	if len(files) == 0 {
		return "", fmt.Errorf("no metadata files matching pattern %s found in mirror directory", metadataPattern)
	}
	// Sort files to get the latest one
	sort.Strings(files)
	latestMetadataName := files[len(files)-1]
	// log.Default().Printf("Latest root.json file: %s\n", latestMetadataName)
	return latestMetadataName, nil
}
