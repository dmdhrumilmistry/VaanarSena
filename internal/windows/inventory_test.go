package windows

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"

	"github.com/dmdhrumilmistry/VaanarSena/internal/command"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// resultsOf flattens the Results elements of a device message the way
// recordResults does.
func resultsOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var msg syncML
	if err := xml.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	for _, res := range msg.Body.Results {
		for _, it := range res.Items {
			out[it.Source.LocURI] = it.Data
		}
	}
	return out
}

const msiListReply = `<SyncML xmlns="SYNCML:SYNCML1.2"><SyncHdr><VerDTD>1.2</VerDTD><VerProto>DM/1.2</VerProto><SessionID>7</SessionID><MsgID>2</MsgID>
<Target><LocURI>https://mdm.example.com/ManagementServer/MDM.svc</LocURI></Target><Source><LocURI>ABC123</LocURI></Source></SyncHdr>
<SyncBody>
<Status><CmdID>1</CmdID><MsgRef>1</MsgRef><CmdRef>0</CmdRef><Cmd>SyncHdr</Cmd><Data>200</Data></Status>
<Status><CmdID>2</CmdID><MsgRef>1</MsgRef><CmdRef>3</CmdRef><Cmd>Get</Cmd><Data>200</Data></Status>
<Results><CmdID>3</CmdID><MsgRef>1</MsgRef><CmdRef>3</CmdRef><Item><Source><LocURI>./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI</LocURI></Source>
<Meta><Format xmlns="syncml:metinf">node</Format></Meta>
<Data>{23170F69-40C1-2702-2301-000001000000}/{AC76BA86-7AD7-1033-7B44-AC0F074E4100}/not-a-guid/{23170F69-40C1-2702-2301-000001000000}</Data></Item></Results>
<Final/></SyncBody></SyncML>`

const msiDetailReply = `<SyncML xmlns="SYNCML:SYNCML1.2"><SyncHdr><VerDTD>1.2</VerDTD><VerProto>DM/1.2</VerProto><SessionID>7</SessionID><MsgID>3</MsgID>
<Target><LocURI>https://mdm.example.com/ManagementServer/MDM.svc</LocURI></Target><Source><LocURI>ABC123</LocURI></Source></SyncHdr>
<SyncBody>
<Results><CmdID>2</CmdID><MsgRef>2</MsgRef><CmdRef>1</CmdRef><Item><Source><LocURI>./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI/%7B23170F69-40C1-2702-2301-000001000000%7D/Name</LocURI></Source>
<Meta><Format xmlns="syncml:metinf">chr</Format></Meta><Data>7-Zip 23.01 (x64)</Data></Item></Results>
<Results><CmdID>3</CmdID><MsgRef>2</MsgRef><CmdRef>2</CmdRef><Item><Source><LocURI>./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI/{23170F69-40C1-2702-2301-000001000000}/Version</LocURI></Source>
<Meta><Format xmlns="syncml:metinf">chr</Format></Meta><Data>23.01.00.0</Data></Item></Results>
<Results><CmdID>4</CmdID><MsgRef>2</MsgRef><CmdRef>3</CmdRef><Item><Source><LocURI>./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI/{23170F69-40C1-2702-2301-000001000000}/Publisher</LocURI></Source>
<Meta><Format xmlns="syncml:metinf">chr</Format></Meta><Data>Igor Pavlov</Data></Item></Results>
<Results><CmdID>5</CmdID><MsgRef>2</MsgRef><CmdRef>4</CmdRef><Item><Source><LocURI>./Device/Vendor/MSFT/EnterpriseDesktopAppManagement/MSI/{AC76BA86-7AD7-1033-7B44-AC0F074E4100}/Version</LocURI></Source>
<Meta><Format xmlns="syncml:metinf">chr</Format></Meta><Data>24.002.20933</Data></Item></Results>
<Final/></SyncBody></SyncML>`

func TestParseMSIProducts(t *testing.T) {
	got := parseMSIProducts(resultsOf(t, msiListReply))
	want := []string{"{23170F69-40C1-2702-2301-000001000000}", "{AC76BA86-7AD7-1033-7B44-AC0F074E4100}"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("products = %v (duplicates and malformed codes must be dropped)", got)
	}
	if parseMSIProducts(map[string]any{}) != nil {
		t.Error("products invented from an empty answer")
	}
}

func TestParseMSIProductsIsCapped(t *testing.T) {
	var codes []string
	for i := 0; i < 600; i++ {
		codes = append(codes, fmt.Sprintf("{00000000-0000-0000-0000-%012d}", i))
	}
	got := parseMSIProducts(map[string]any{msiBase: strings.Join(codes, "/")})
	if len(got) != maxMSIProducts {
		t.Errorf("got %d products, want %d", len(got), maxMSIProducts)
	}
}

func TestMSIItems(t *testing.T) {
	products := parseMSIProducts(resultsOf(t, msiListReply))
	items := msiItems(resultsOf(t, msiDetailReply), products)
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	// Results may name a node with escaped or literal braces.
	seven := items[0]
	if seven.Name != "7-Zip 23.01 (x64)" || seven.Version != "23.01.00.0" || seven.Publisher != "Igor Pavlov" ||
		seven.Identifier != products[0] || seven.Source != "msi" || seven.State != "installed" {
		t.Errorf("7-zip: %+v", seven)
	}
	// A product whose Name is missing (404) falls back to its ProductCode.
	if r := items[1]; r.Name != products[1] || r.Version != "24.002.20933" {
		t.Errorf("reader: %+v", r)
	}
}

func TestMSIInventoryCommands(t *testing.T) {
	d := &Driver{}
	dev := &store.Device{Platform: store.PlatformWindows}
	out := newReply("7", "1", "https://mdm/ManagementServer/MDM.svc", "ABC123")
	if err := d.translate(t.Context(), dev, &store.Command{Type: command.MSIInventory}, out); err != nil {
		t.Fatal(err)
	}
	body := out.finish()
	if !strings.Contains(body, "<Get><CmdID>1</CmdID><Item><Target><LocURI>"+msiBase+"</LocURI>") {
		t.Errorf("msi_inventory did not Get the MSI node: %s", body)
	}

	out = newReply("7", "2", "https://mdm/ManagementServer/MDM.svc", "ABC123")
	params, _ := json.Marshal(command.Params{Products: []string{"{23170F69-40C1-2702-2301-000001000000}"}})
	if err := d.translate(t.Context(), dev, &store.Command{Type: command.MSIInventoryDetail, Params: params}, out); err != nil {
		t.Fatal(err)
	}
	body = out.finish()
	for _, leaf := range []string{"Name", "Version", "Publisher"} {
		if !strings.Contains(body, msiBase+"/%7B23170F69-40C1-2702-2301-000001000000%7D/"+leaf+"</LocURI>") {
			t.Errorf("missing Get for %s: %s", leaf, body)
		}
	}
	var msg syncML
	if err := xml.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("reply is not valid XML: %v", err)
	}
}

func TestMSIInventoryNotAllowedOnPersonalDevices(t *testing.T) {
	personal := &store.Device{Platform: store.PlatformWindows, Ownership: store.OwnershipPersonal, Status: store.StatusEnrolled}
	corporate := &store.Device{Platform: store.PlatformWindows, Ownership: store.OwnershipCorporate, Status: store.StatusEnrolled}
	for _, typ := range []string{command.MSIInventory, command.MSIInventoryDetail} {
		if err := command.AuthorizeInternal(personal, typ); err == nil {
			t.Errorf("%s allowed on a personal device", typ)
		}
		if err := command.AuthorizeInternal(corporate, typ); err != nil {
			t.Errorf("%s refused on a corporate device: %v", typ, err)
		}
		if _, ok := command.Lookup(typ); ok {
			t.Errorf("%s is in the user-facing catalogue", typ)
		}
	}
}

// appxReply returns AppInventoryResults with the given Data content.
func appxReply(data string) string {
	return `<SyncML xmlns="SYNCML:SYNCML1.2"><SyncHdr><VerDTD>1.2</VerDTD><VerProto>DM/1.2</VerProto><SessionID>7</SessionID><MsgID>2</MsgID>
<Target><LocURI>https://mdm.example.com/ManagementServer/MDM.svc</LocURI></Target><Source><LocURI>ABC123</LocURI></Source></SyncHdr>
<SyncBody>
<Results><CmdID>4</CmdID><MsgRef>1</MsgRef><CmdRef>5</CmdRef><Item><Source><LocURI>` + appxResultsNode + `</LocURI></Source>
<Meta><Format xmlns="syncml:metinf">xml</Format></Meta><Data>` + data + `</Data></Item></Results>
<Final/></SyncBody></SyncML>`
}

const appxInventory = `<Packages>` +
	`<Package Name="Microsoft.WindowsTerminal" PackageFullName="Microsoft.WindowsTerminal_1.21.2361.0_x64__8wekyb3d8bbwe" PackageFamilyName="Microsoft.WindowsTerminal_8wekyb3d8bbwe" Version="1.21.2361.0" Publisher="CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US" PackageStatus="0" IsFramework="0" IsBundle="0"/>` +
	`<Package PackageFullName="Contoso.LineOfBusiness_3.2.0.0_neutral__abcdefghjk" PackageFamilyName="Contoso.LineOfBusiness_abcdefghjk" PackageStatus="2"/>` +
	`<Package Name="Microsoft.VCLibs.140.00" PackageFullName="Microsoft.VCLibs.140.00_14.0.33519.0_x64__8wekyb3d8bbwe" PackageFamilyName="Microsoft.VCLibs.140.00_8wekyb3d8bbwe" IsFramework="1"/>` +
	`<Package Name="Microsoft.WindowsTerminal" PackageFullName="Microsoft.WindowsTerminal_1.21.2361.0_x64__8wekyb3d8bbwe" PackageFamilyName="Microsoft.WindowsTerminal_8wekyb3d8bbwe"/>` +
	`</Packages>`

func TestAppxItems(t *testing.T) {
	escaped := strings.NewReplacer("<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(appxInventory)
	for name, data := range map[string]string{"embedded": appxInventory, "escaped": escaped} {
		t.Run(name, func(t *testing.T) {
			got := appxItems(resultsOf(t, appxReply(data)))
			if len(got) != 2 {
				t.Fatalf("got %d items, want 2 (frameworks and duplicates dropped): %+v", len(got), got)
			}
			term, lob := got[0], got[1]
			if term.Name != "Microsoft.WindowsTerminal" || term.Identifier != "Microsoft.WindowsTerminal_8wekyb3d8bbwe" ||
				term.Version != "1.21.2361.0" || !strings.HasPrefix(term.Publisher, "CN=Microsoft") || term.Source != "appx" || term.State != "installed" {
				t.Errorf("terminal = %+v", term)
			}
			// No Name or Version attributes: fall back to the family name and
			// the version inside PackageFullName; a modified package is flagged.
			if lob.Name != "Contoso.LineOfBusiness_abcdefghjk" || lob.Version != "3.2.0.0" || lob.State != "needs attention" {
				t.Errorf("line of business app = %+v", lob)
			}
		})
	}
	if appxItems(map[string]any{}) != nil {
		t.Error("no results should give no items")
	}
}

func TestMSIInventoryQueriesPackagedApps(t *testing.T) {
	d := &Driver{}
	dev := &store.Device{Platform: store.PlatformWindows}
	out := newReply("7", "1", "https://mdm/ManagementServer/MDM.svc", "ABC123")
	if err := d.translate(t.Context(), dev, &store.Command{Type: command.MSIInventory}, out); err != nil {
		t.Fatal(err)
	}
	body := out.finish()
	q := strings.Index(body, "<Replace>")
	g := strings.Index(body, "<LocURI>"+appxResultsNode+"</LocURI>")
	if q < 0 || g < 0 || g < q {
		t.Fatalf("expected Replace AppInventoryQuery before Get AppInventoryResults: %s", body)
	}
	if !strings.Contains(body, appxQueryNode) || !strings.Contains(body, `<Format xmlns="syncml:metinf">xml</Format>`) ||
		!strings.Contains(strings.NewReplacer("&#34;", `"`, "&quot;", `"`).Replace(body), `Output="PackageDetails"`) {
		t.Errorf("query not sent as documented: %s", body)
	}
	var msg syncML
	if err := xml.Unmarshal([]byte(body), &msg); err != nil {
		t.Fatalf("reply is not valid XML: %v", err)
	}
}
