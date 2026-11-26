package main

import (
	"agent/pathcheck"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	"github.com/google/uuid"
)

type pathCheckStore struct{ agent *agentsdk.Agent }

func (s pathCheckStore) Save(ctx context.Context, device string, data []byte) (string, error) {
	user, ok := agentsdk.CallerFromContext(ctx).User()
	if !ok || user.ID == "" {
		return "", errors.New("Airlock user required")
	}
	path := fmt.Sprintf("tmp/path-check/%s/%s.png", uuid.NewString(), device)
	resolved, err := s.agent.ResolveFilePath(ctx, path, agentsdk.FileOperationWrite)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(string(resolved), "tmp/path-check/user-"+user.ID+"/") {
		return "", errors.New("caller scope unavailable")
	}
	if _, err = s.agent.WriteFile(ctx, string(resolved), bytes.NewReader(data), "image/png"); err != nil {
		return "", err
	}
	ref, err := s.agent.StatFileRef(ctx, string(resolved))
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(ref.Path, "/apps/") {
		return "", errors.New("catalog path unavailable")
	}
	return ref.Path, nil
}

func registerPathCheck(a *agentsdk.Agent) {
	a.RegisterDirectory("tmp/path-check", agentsdk.DirectoryOpts{Read: agentsdk.AccessUser, Write: agentsdk.AccessUser, List: agentsdk.AccessUser, Scope: agentsdk.ScopeUser, RetentionHours: 72, Description: "Caller-scoped temporary purchase path screenshots"})
	service := pathcheck.New(pathcheck.CommandRunner{}, pathCheckStore{a})
	a.RegisterTool(tool.Typed[pathcheck.Input, pathcheck.Batch]("check_purchase_path").
		Description("Check the fixed public Kaspersky purchase path on desktop and mobile. Screenshot paths are private to the caller and expire after 72 hours.").
		Execute(func(ctx context.Context, in pathcheck.Input) (pathcheck.Batch, error) {
			if _, ok := agentsdk.CallerFromContext(ctx).User(); !ok {
				return pathcheck.Batch{}, errors.New("Airlock user required")
			}
			return service.Check(ctx, in)
		}).Build(), agentsdk.AccessUser)
}
