package provider

import (
	"context"
	"errors"
	"net/http"

	sdk "github.com/clo-ru/cloapi-go-client/v3"
	"github.com/google/uuid"
)

// ProjectInventory is read-only and complete; no caller may infer absence from a partial page.
type ProjectInventory struct {
	Servers   []sdk.ServerSchema
	Volumes   []sdk.VolumeSchema
	Addresses []sdk.AddressSchema
}

func (c *Client) Inventory(ctx context.Context, project string) (ProjectInventory, error) {
	var out ProjectInventory
	if _, err := uuid.Parse(project); err != nil {
		return out, errors.New("invalid inventory project")
	}
	response, err := c.sdk.ProjectDetail(ctx, project)
	detail, err := read[struct {
		Result *sdk.ProjectDetailSchema `json:"result"`
	}](response, err)
	if err != nil {
		return out, err
	}
	if detail.Result == nil || detail.Result.Id != project || detail.Result.Status != "ACTIVE" {
		return out, errors.New("inventory project identity/status changed")
	}
	out.Servers, err = complete[sdk.ServerSchema](ctx, func(offset int) (*http.Response, error) {
		return c.sdk.ProjectServerList(ctx, project, sdk.WithPage(100, offset))
	}, func(s sdk.ServerSchema) string { return s.Id })
	if err != nil {
		return out, err
	}
	for _, server := range out.Servers {
		if server.Project != project {
			return out, errors.New("foreign server in inventory")
		}
	}
	out.Volumes, err = complete[sdk.VolumeSchema](ctx, func(offset int) (*http.Response, error) {
		return c.sdk.ProjectVolumesList(ctx, project, sdk.WithPage(100, offset))
	}, func(v sdk.VolumeSchema) string { return v.Id })
	if err != nil {
		return out, err
	}
	out.Addresses, err = complete[sdk.AddressSchema](ctx, func(offset int) (*http.Response, error) {
		return c.sdk.ProjectAddressesList(ctx, project, sdk.WithPage(100, offset))
	}, func(a sdk.AddressSchema) string { return a.Id })
	return out, err
}
