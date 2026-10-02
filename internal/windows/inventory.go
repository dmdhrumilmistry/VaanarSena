package windows

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"regexp"
	"strings"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// Software inventory on Windows is limited to what MDM CSPs expose.
//
// Implemented: MSI products, read from the EnterpriseDesktopAppManagement CSP.
// A Get on the interior node ./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI
// returns the ProductCodes of the child nodes (separated by "/"); the Name,
// Version and Publisher leaves of each child are then read in a second
// command, capped at maxMSIProducts. As documented, that CSP reports MSI
// installs it can see; it is not a complete Add/Remove Programs listing, and
// a build that lacks the nodes answers 404, which is treated as unavailable.
//
// Packaged (Microsoft Store and sideloaded AppX/MSIX) apps come from the
// EnterpriseModernAppManagement CSP: msi_inventory also writes an
// AppInventoryQuery (Output=PackageDetails, main packages from the Store and
// other sources, not OS components) and reads AppInventoryResults in the same
// message. Microsoft does not document the result schema in detail, so the
// parser reads any element that carries a PackageFullName or
// PackageFamilyName attribute, whatever its parent is called. Services have
// no MDM CSP and are never collected.
//
// Personal devices are never read: the msi_inventory commands are refused by
// command.AuthorizeInternal for personal ownership.
const (
	msiBase        = "./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI"
	maxMSIProducts = 500

	appxQueryNode   = "./Device/Vendor/MSFT/EnterpriseModernAppManagement/AppManagement/AppInventoryQuery"
	appxResultsNode = "./Device/Vendor/MSFT/EnterpriseModernAppManagement/AppManagement/AppInventoryResults"
	appxQuery       = `<Inventory Output="PackageDetails" Source="AppStore|nonStore" PackageTypeFilter="Main"/>`
	maxAppxPackages = 2000
)

// appxItems parses AppInventoryResults. The data may arrive escaped or as
// embedded XML; both reach here as markup text.
func appxItems(results map[string]any) []store.InventoryItem {
	raw, ok := normalizeResults(results)[appxResultsNode]
	if !ok || !strings.Contains(raw, "<") {
		return nil
	}
	dec := xml.NewDecoder(strings.NewReader(raw))
	var out []store.InventoryItem
	seen := map[string]bool{}
	for len(out) < maxAppxPackages {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		attr := map[string]string{}
		for _, a := range se.Attr {
			attr[a.Name.Local] = strings.TrimSpace(a.Value)
		}
		full, family := attr["PackageFullName"], attr["PackageFamilyName"]
		if full == "" && family == "" {
			continue
		}
		id := family
		if id == "" {
			id = full
		}
		if seen[id] || attr["IsFramework"] == "1" || attr["IsBundle"] == "1" || attr["IsStub"] == "1" {
			continue
		}
		seen[id] = true
		name := attr["Name"]
		if name == "" {
			name = id
		}
		version := attr["Version"]
		if version == "" && full != "" {
			// PackageFullName is Name_Version_Architecture_ResourceId_PublisherId.
			if parts := strings.Split(full, "_"); len(parts) >= 2 {
				version = parts[1]
			}
		}
		state := "installed"
		if st := attr["PackageStatus"]; st != "" && st != "0" {
			state = "needs attention"
		}
		out = append(out, store.InventoryItem{
			Name: name, Identifier: id, Version: version, Publisher: attr["Publisher"],
			Source: "appx", State: state,
		})
	}
	return out
}

// productCode is the form of an MSI ProductCode node name.
var productCode = regexp.MustCompile(`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`)

// msiLeaves are the per-product nodes read by msi_inventory_detail.
var msiLeaves = []string{"Name", "Version", "Publisher"}

// unescapeURI undoes the escaping of braces in ProductCode node names, so a
// device may answer with either form.
var uriUnescaper = strings.NewReplacer("%7B", "{", "%7b", "{", "%7D", "}", "%7d", "}")

func normalizeResults(results map[string]any) map[string]string {
	out := make(map[string]string, len(results))
	for k, v := range results {
		if s, ok := v.(string); ok {
			out[strings.TrimRight(uriUnescaper.Replace(k), "/")] = s
		}
	}
	return out
}

// parseMSIProducts extracts the ProductCodes from the answer to a Get on the
// MSI interior node, limited to maxMSIProducts.
func parseMSIProducts(results map[string]any) []string {
	raw, ok := normalizeResults(results)[msiBase]
	if !ok {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, code := range strings.Split(raw, "/") {
		code = strings.TrimSpace(uriUnescaper.Replace(code))
		if !productCode.MatchString(code) || seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
		if len(out) == maxMSIProducts {
			break
		}
	}
	return out
}

// msiItems turns the answers to the per-product Gets into inventory items.
// Products whose Name node is absent (404) are shown by ProductCode.
func msiItems(results map[string]any, products []string) []store.InventoryItem {
	res := normalizeResults(results)
	out := make([]store.InventoryItem, 0, len(products))
	for _, code := range products {
		node := func(leaf string) string { return strings.TrimSpace(res[msiBase+"/"+code+"/"+leaf]) }
		name := node("Name")
		if name == "" {
			name = code
		}
		out = append(out, store.InventoryItem{
			Name: name, Identifier: code, Version: node("Version"), Publisher: node("Publisher"),
			Source: "msi", State: "installed",
		})
	}
	return out
}

// queueMSIInventory starts the MSI read after a Refresh on a corporate device.
func (d *Driver) queueMSIInventory(ctx context.Context, dev *store.Device) {
	if err := d.Svc.EnqueueInternal(ctx, dev, command.MSIInventory, nil); err != nil {
		d.Log.Debug("queue msi inventory", "device", dev.ID, "err", err)
	}
}

func (d *Driver) afterInventory(ctx context.Context, dev *store.Device, c *store.Command, results map[string]any) {
	if dev.IsPersonal() {
		return // defence in depth: these commands are not queued for personal devices
	}
	switch c.Type {
	case command.MSIInventory:
		appx := appxItems(results)
		products := parseMSIProducts(results)
		if len(products) == 0 {
			// No MSI products (or the CSP is absent): store the packaged apps if
			// any were reported; otherwise leave what we have.
			if len(appx) > 0 {
				if err := d.Store.ReplaceInventory(ctx, dev.ID, store.KindApp, appx); err != nil {
					d.Log.Warn("store windows inventory", "device", dev.ID, "err", err)
				}
			}
			return
		}
		params, _ := json.Marshal(command.Params{Products: products, Inventory: appx})
		if err := d.Svc.EnqueueInternal(ctx, dev, command.MSIInventoryDetail, params); err != nil {
			d.Log.Warn("queue msi inventory detail", "device", dev.ID, "err", err)
		}
	case command.MSIInventoryDetail:
		p, err := command.ParseParams(c.Params)
		if err != nil {
			return
		}
		items := append(msiItems(results, p.Products), p.Inventory...)
		if err := d.Store.ReplaceInventory(ctx, dev.ID, store.KindApp, items); err != nil {
			d.Log.Warn("store windows inventory", "device", dev.ID, "err", err)
		}
	}
}
