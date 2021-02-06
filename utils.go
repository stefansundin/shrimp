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

func min(a, b int64) int64 {
	if a > b {
		return b
	}
	return a
}

func parseLimit(s string) (int64, error) {
	factor := 1
	suffix := s[len(s)-1]
	if suffix == 'k' {
		factor = 1024
	} else if suffix == 'm' {
		factor = 1024 * 1024
	} else if suffix == 'g' {
		// If you have any use of this then you are lucky and I am jealous :)
		factor = 1024 * 1024 * 1024
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
