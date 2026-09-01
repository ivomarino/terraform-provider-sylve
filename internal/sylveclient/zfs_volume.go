package sylveclient

import (
	"context"
	"fmt"
)

// CreateVolume creates a ZFS volume (zvol) at parent+"/"+name. Unlike
// CreateFilesystem, "parent" is NOT buggy here -- the service genuinely
// uses the top-level parent argument, no properties-map duplication
// needed. But properties MUST include a "size" key as a human-readable
// string (e.g. "10G", "500M" -- parsed via HumanFormatToSize), or the
// service rejects it with "size property not found"; there is no
// separate structured size field on the request.
func (c *Client) CreateVolume(ctx context.Context, name, parent string, properties map[string]string) error {
	if _, ok := properties["size"]; !ok {
		return fmt.Errorf("creating ZFS volume %q under %q: properties[\"size\"] is required (e.g. \"10G\") -- Sylve has no separate structured size field", name, parent)
	}
	body := map[string]any{
		"name":       name,
		"parent":     parent,
		"properties": properties,
	}
	if err := c.do(ctx, "POST", "/api/zfs/datasets/volume", body, nil); err != nil {
		return fmt.Errorf("creating ZFS volume %q under %q: %w", name, parent, err)
	}
	return nil
}

// EditVolume sets properties on an existing volume. PATCH
// /api/zfs/datasets/volume -- guid identifies the target, not a URL
// param, same shape as EditFilesystem.
func (c *Client) EditVolume(ctx context.Context, guid string, properties map[string]string) error {
	return c.do(ctx, "PATCH", "/api/zfs/datasets/volume",
		map[string]any{"guid": guid, "properties": properties}, nil)
}

// DeleteVolume removes a volume by GUID.
func (c *Client) DeleteVolume(ctx context.Context, guid string) error {
	return c.do(ctx, "DELETE", "/api/zfs/datasets/volume/"+guid, nil, nil)
}

// FlashVolume writes a download's raw bytes directly onto an existing
// volume -- literally a `dd`, per the source's own naming and behavior.
// POST /api/zfs/datasets/volume/flash. This is the actual mechanism for
// getting a downloaded cloud image's contents onto a real ZFS volume;
// there is no "create volume from image" combined operation, and no
// per-VM copy-on-write semantics here -- flashing writes the image once,
// onto that one volume.
func (c *Client) FlashVolume(ctx context.Context, guid, downloadUUID string) error {
	return c.do(ctx, "POST", "/api/zfs/datasets/volume/flash",
		map[string]string{"guid": guid, "uuid": downloadUUID}, nil)
}
