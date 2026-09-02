package sylveclient

import (
	"context"
	"fmt"
)

// Dataset is trimmed to what this client currently surfaces. The real
// GET /api/zfs/datasets response is considerably richer (every ZFS
// property, each with its own {value, source} pair distinguishing
// LOCAL/INHERITED/DEFAULT/NONE) -- this client only pulls out the handful
// of fields needed to confirm a filesystem exists and locate it by name,
// deliberately not modeling the full property set (see
// FilesystemResourceModel's own doc comment on why `properties` is
// write-only).
type Dataset struct {
	Name       string `json:"name"`
	GUID       string `json:"guid"`
	Type       string `json:"type"` // "FILESYSTEM", "VOLUME", "SNAPSHOT"
	Pool       string `json:"pool"`
	Mountpoint string `json:"mountpoint"`
}

type datasetListEnvelope struct {
	Data []Dataset `json:"data"`
}

// CreateFilesystem creates a ZFS filesystem dataset at parent+"/"+name.
//
// Sylve's CreateFilesystemRequest has a genuinely surprising bug/quirk,
// confirmed against a live Sylve instance (see the provider's dev
// notes): the top-level "parent" field is gin-binding required but
// completely UNUSED server-side -- internal/services/zfs/dataset_fs.go's
// CreateFilesystem instead reads a same-named "parent" key OUT OF the
// properties map to build the full dataset path, then deletes it from
// the map before actually creating the dataset. So the parent value must
// be sent TWICE: once at the top level (to satisfy validation, otherwise
// discarded) and once inside properties (the one that's actually used).
// This method hides that behind a single `parent` argument.
func (c *Client) CreateFilesystem(ctx context.Context, name, parent string, properties map[string]string) error {
	props := make(map[string]string, len(properties)+1)
	for k, v := range properties {
		props[k] = v
	}
	props["parent"] = parent

	body := map[string]any{
		"name":       name,
		"parent":     parent, // required by binding, ignored by the service -- see doc comment
		"properties": props,
	}
	if err := c.do(ctx, "POST", "/api/zfs/datasets/filesystem", body, nil); err != nil {
		return fmt.Errorf("creating ZFS filesystem %q under %q: %w", name, parent, err)
	}
	return nil
}

// EditFilesystem sets properties on an existing filesystem dataset.
// v0.3.0: PATCH /api/zfs/datasets/filesystem/{guid} -- guid moved into
// the URL path (v0.2.x carried it in the body instead, flat
// PATCH /api/zfs/datasets/filesystem). Also gained a new
// ReplicationDatasetMutationGuard middleware server-side, which should
// be transparent unless replication (an opt-in v0.3.x feature) is
// active on this dataset.
func (c *Client) EditFilesystem(ctx context.Context, guid string, properties map[string]string) error {
	return c.do(ctx, "PATCH", "/api/zfs/datasets/filesystem/"+guid,
		map[string]any{"properties": properties}, nil)
}

// DeleteFilesystem removes a filesystem dataset by GUID.
func (c *Client) DeleteFilesystem(ctx context.Context, guid string) error {
	return c.do(ctx, "DELETE", "/api/zfs/datasets/filesystem/"+guid, nil, nil)
}

// ListDatasets returns every dataset (filesystem, volume, and snapshot)
// on the node. GET /api/zfs/datasets.
func (c *Client) ListDatasets(ctx context.Context) ([]Dataset, error) {
	var out datasetListEnvelope
	if err := c.do(ctx, "GET", "/api/zfs/datasets", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// GetDatasetByGUID finds a dataset by GUID. There is no per-GUID GET
// endpoint, only the combined list, so this lists and filters. Returns
// an error satisfying IsNotFound if no dataset has that GUID.
func (c *Client) GetDatasetByGUID(ctx context.Context, guid string) (*Dataset, error) {
	datasets, err := c.ListDatasets(ctx)
	if err != nil {
		return nil, err
	}
	for _, ds := range datasets {
		if ds.GUID == guid {
			return &ds, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("dataset guid %q not found", guid)}
}

// GetDatasetByName finds a dataset by its full "pool/path" name -- used
// right after CreateFilesystem, whose response doesn't carry the new
// dataset's GUID.
func (c *Client) GetDatasetByName(ctx context.Context, fullName string) (*Dataset, error) {
	datasets, err := c.ListDatasets(ctx)
	if err != nil {
		return nil, err
	}
	for _, ds := range datasets {
		if ds.Name == fullName {
			return &ds, nil
		}
	}
	return nil, &apiError{StatusCode: 404, Body: fmt.Sprintf("dataset %q not found after create", fullName)}
}
