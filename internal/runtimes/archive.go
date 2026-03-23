package runtimes

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractArchive(destinationDir string, archivePath string, format ArchiveFormat) error {
	switch format {
	case ArchiveFormatZip:
		return extractZip(destinationDir, archivePath)
	case ArchiveFormatTarGz:
		return extractTarGz(destinationDir, archivePath)
	default:
		return fmt.Errorf("unsupported archive format %q", format)
	}
}

func extractZip(destinationDir string, archivePath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		targetPath, err := safeJoin(destinationDir, file.Name)
		if err != nil {
			return err
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
			return err
		}

		source, err := file.Open()
		if err != nil {
			return err
		}

		mode := file.Mode()
		if mode == 0 {
			mode = 0o644
		}

		destination, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			source.Close()
			return err
		}

		if _, err := io.Copy(destination, source); err != nil {
			destination.Close()
			source.Close()
			return err
		}

		if err := destination.Close(); err != nil {
			source.Close()
			return err
		}
		if err := source.Close(); err != nil {
			return err
		}
	}

	return nil
}

func extractTarGz(destinationDir string, archivePath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		targetPath, err := safeJoin(destinationDir, header.Name)
		if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}

			mode := os.FileMode(header.Mode)
			if mode == 0 {
				mode = 0o644
			}

			destination, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}

			if _, err := io.Copy(destination, reader); err != nil {
				destination.Close()
				return err
			}

			if err := destination.Close(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported tar entry type %d", header.Typeflag)
		}
	}
}

func safeJoin(baseDir string, archivePath string) (string, error) {
	targetPath := filepath.Join(baseDir, filepath.FromSlash(archivePath))
	cleanBase := filepath.Clean(baseDir)
	cleanTarget := filepath.Clean(targetPath)

	if cleanTarget == cleanBase {
		return cleanTarget, nil
	}

	prefix := cleanBase + string(os.PathSeparator)
	if !strings.HasPrefix(cleanTarget, prefix) {
		return "", fmt.Errorf("archive entry escapes destination: %s", archivePath)
	}

	return cleanTarget, nil
}
