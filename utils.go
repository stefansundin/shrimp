package main

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"

	s3Types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const kiB int64 = 1024
const MiB int64 = 1024 * kiB
const GiB int64 = 1024 * MiB
const TiB int64 = 1024 * GiB

func min(a, b int64) int64 {
	if a > b {
		return b
	}
	return a
}

func parseRate(s string) (int64, error) {
	factor := 1
	suffix := s[len(s)-1]
	if suffix == 'k' || suffix == 'K' {
		factor = 1e3
	} else if suffix == 'm' || suffix == 'M' {
		factor = 1e6
	} else if suffix == 'g' || suffix == 'G' {
		// If you have any use of this then you are lucky and I am jealous :)
		factor = 1e9
	}
	if factor != 1 {
		s = s[0 : len(s)-1]
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int64(math.Round(f * float64(factor))), nil
}

func formatSize(size int64) string {
	if size < 1e3 {
		return fmt.Sprintf("%d B", size)
	} else if size < 1e6 {
		return fmt.Sprintf("%.1f kB", float64(size)/1e3)
	} else if size < 1e9 {
		return fmt.Sprintf("%.1f MB", float64(size)/1e6)
	} else if size < 1e12 {
		return fmt.Sprintf("%.1f GB", float64(size)/1e9)
	} else {
		return fmt.Sprintf("%.1f TB", float64(size)/1e12)
	}
}

// The S3 docs state GB and TB but they actually mean GiB and TiB
// For consistency, format filesizes in GiB and TiB
func formatFilesize(size int64) string {
	if size < kiB {
		return fmt.Sprintf("%d B", size)
	} else if size < MiB {
		return fmt.Sprintf("%.1f kiB (%d bytes)", float64(size)/float64(kiB), size)
	} else if size < GiB {
		return fmt.Sprintf("%.1f MiB (%d bytes)", float64(size)/float64(MiB), size)
	} else if size < TiB {
		return fmt.Sprintf("%.1f GiB (%d bytes)", float64(size)/float64(GiB), size)
	} else {
		return fmt.Sprintf("%.1f TiB (%d bytes)", float64(size)/float64(TiB), size)
	}
}

func getSha256Sum(sumsFn string, entryPath string) (string, error) {
	if err := unix.Access(sumsFn, unix.R_OK); err != nil {
		return "", err
	}
	entryPath, err := filepath.Abs(entryPath)

	file, err := os.Open(sumsFn)
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		sum := line[0:64]
		mid := line[64:66]
		if mid != "  " && mid != " *" {
			return "", errors.New("Unsupported SHA256SUMS format")
		}
		path, err := filepath.Abs(line[66:])
		if err != nil {
			return "", err
		}
		if path == entryPath {
			return sum, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	return "", nil
}

func validStorageClass(s string) (s3Types.StorageClass, error) {
	sc := s3Types.StorageClass(s)
	values := sc.Values()
	for _, v := range values {
		if v == sc {
			return v, nil
		}
	}
	return sc, errors.New(fmt.Sprintf("Invalid --storage-class. Supported values: %v", values))
}
