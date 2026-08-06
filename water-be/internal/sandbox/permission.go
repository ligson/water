package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/ligson/water/water-be/internal/workspace"
)

type AccessMode string

const (
	AccessRead  AccessMode = "read"
	AccessWrite AccessMode = "write"
)

var ErrAccessDenied = errors.New("path access denied")

type PermissionEngine struct {
	workspaceStore *workspace.Store
}

func NewPermissionEngine(workspaceStore *workspace.Store) *PermissionEngine {
	return &PermissionEngine{workspaceStore: workspaceStore}
}

func (p *PermissionEngine) CheckPath(ctx context.Context, ws workspace.Workspace, target string, mode AccessMode) (string, error) {
	cleanTarget, err := cleanAbs(target)
	if err != nil {
		return "", err
	}
	cleanRoot, err := cleanAbs(ws.RootPath)
	if err != nil {
		return "", err
	}
	if isWithin(cleanRoot, cleanTarget) {
		return cleanTarget, nil
	}

	externalPaths, err := p.workspaceStore.ListExternalPaths(ctx, ws.ID)
	if err != nil {
		return "", err
	}
	for _, item := range externalPaths {
		allowedPath, err := cleanAbs(item.Path)
		if err != nil {
			continue
		}
		if !pathModeAllows(item.AccessMode, mode) {
			continue
		}
		if item.PathType == workspace.PathTypeFile && cleanTarget == allowedPath {
			return cleanTarget, nil
		}
		if item.PathType == workspace.PathTypeDirectory && isWithin(allowedPath, cleanTarget) {
			return cleanTarget, nil
		}
	}

	return "", ErrAccessDenied
}

func cleanAbs(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ErrAccessDenied
	}
	cleaned := filepath.Clean(target)
	if !filepath.IsAbs(cleaned) {
		return "", ErrAccessDenied
	}
	return cleaned, nil
}

func isWithin(root string, target string) bool {
	if root == target {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

func pathModeAllows(granted string, requested AccessMode) bool {
	if requested == AccessRead {
		return granted == workspace.AccessModeRead || granted == workspace.AccessModeWrite
	}
	return granted == workspace.AccessModeWrite
}
