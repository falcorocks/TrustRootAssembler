package main

import (
	"os"
	"testing"
)

func TestDownloadFile(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{
			name:    "valid URL",
			url:     "https://tuf-repo-cdn.sigstore.dev/timestamp.json",
			wantErr: false,
		},
		{
			name:    "invalid URL",
			url:     "https://invalid-domain-that-does-not-exist.example/file.json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file to download the file to
			tmpfile, err := os.CreateTemp("", "test-*")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			// Call the downloadFile function
			err = DownloadFile(tmpfile, tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("downloadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Only check file contents if we didn't expect an error
				downloadedData, err := os.ReadFile(tmpfile.Name())
				if err != nil {
					t.Fatalf("Failed to read downloaded file: %v", err)
				}

				if len(downloadedData) == 0 {
					t.Errorf("downloadFile() downloaded empty file")
				}
				t.Logf("downloaded file size: %d bytes", len(downloadedData))
			}
		})
	}
}

func TestCompressDirectory(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() (src string, dst string)
		wantErr bool
	}{
		{
			name: "valid directory",
			setup: func() (string, string) {
				src, _ := os.MkdirTemp("", "src-*")
				os.WriteFile(src+"/file1.txt", []byte("content1"), os.ModePerm)
				os.WriteFile(src+"/file2.txt", []byte("content2"), os.ModePerm)
				dst, _ := os.CreateTemp("", "output-*.tar.gz")
				dst.Close()
				return src, dst.Name()
			},
			wantErr: false,
		},
		{
			name: "non-existent directory",
			setup: func() (string, string) {
				src := "testdata/nonexistent"
				dst, _ := os.CreateTemp("", "output-*.tar.gz")
				dst.Close()
				return src, dst.Name()
			},
			wantErr: true,
		},
		{
			name: "empty directory",
			setup: func() (string, string) {
				src, _ := os.MkdirTemp("", "empty-*")
				dst, _ := os.CreateTemp("", "output_empty-*.tar.gz")
				dst.Close()
				return src, dst.Name()
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, dst := tt.setup()

			// Run compressDirectory function
			err := CompressDirectory(src, dst)
			if (err != nil) != tt.wantErr {
				t.Errorf("compressDirectory() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Clean up
			os.RemoveAll(src)
			os.RemoveAll(dst)
		})
	}
}

func TestEncodeBase64(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() (*os.File, error)
		want    string
		wantErr bool
	}{
		{
			name: "valid file",
			setup: func() (*os.File, error) {
				tmpfile, err := os.CreateTemp("", "test-*")
				if err != nil {
					return nil, err
				}
				content := []byte("hello world")
				if _, err := tmpfile.Write(content); err != nil {
					return nil, err
				}
				if _, err := tmpfile.Seek(0, 0); err != nil {
					return nil, err
				}
				return tmpfile, nil
			},
			want:    "aGVsbG8gd29ybGQ=",
			wantErr: false,
		},
		{
			name: "empty file",
			setup: func() (*os.File, error) {
				tmpfile, err := os.CreateTemp("", "test-*")
				if err != nil {
					return nil, err
				}
				return tmpfile, nil
			},
			want:    "",
			wantErr: false,
		},
		{
			name: "non-existent file",
			setup: func() (*os.File, error) {
				return os.Open("non-existent-file")
			},
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := tt.setup()
			if err != nil {
				if !tt.wantErr {
					t.Fatalf("setup() error = %v", err)
				}
				return
			}
			defer os.Remove(file.Name())

			got, err := EncodeBase64(file)
			if (err != nil) != tt.wantErr {
				t.Errorf("encodeBase64() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("encodeBase64() = %v, want %v", got, tt.want)
			}
		})
	}
}
