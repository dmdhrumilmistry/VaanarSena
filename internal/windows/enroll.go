// Package windows implements Windows MDM: the MS-MDE2 enrollment services
// (discovery, MS-XCEP enrollment policy, MS-WSTEP enrollment) and the OMA-DM
// 1.2 management endpoint (MS-MDM). Enrollment uses the OnPremise auth policy:
// the user signs in with their email and an enrollment token as password.
package windows

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dmdhrumilmistry/VaanarSena/internal/httpx"
	"github.com/dmdhrumilmistry/VaanarSena/internal/mdm"
	"github.com/dmdhrumilmistry/VaanarSena/internal/pki"
	"github.com/dmdhrumilmistry/VaanarSena/internal/secrets"
	"github.com/dmdhrumilmistry/VaanarSena/internal/store"
)

// ProviderID identifies VaanarSena in the Windows DMClient CSP.
const ProviderID = "VaanarSena"

const maxBody = 4 << 20

// Driver is the Windows MDM driver.
type Driver struct {
	Store      *store.Store
	Svc        *mdm.Service
	CA         *pki.CA
	Org        string
	PublicURL  string
	CertHeader string
	Log        *slog.Logger
}

// Platforms implements mdm.Driver.
func (d *Driver) Platforms() []string { return []string{store.PlatformWindows} }

// Wake is a no-op: Windows devices poll on the DMClient schedule configured at
// enrollment (every 15 minutes). Server-initiated sessions need WNS, which
// requires a Microsoft Store app registration and is not used here.
func (d *Driver) Wake(context.Context, *store.Device) error { return nil }

// Routes registers the enrollment and management endpoints.
func (d *Driver) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /EnrollmentServer/Discovery.svc", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("POST /EnrollmentServer/Discovery.svc", d.handleDiscovery)
	mux.HandleFunc("POST /EnrollmentServer/Policy.svc", d.handlePolicy)
	mux.HandleFunc("POST /EnrollmentServer/Enrollment.svc", d.handleEnrollment)
	mux.HandleFunc("POST /ManagementServer/MDM.svc", d.handleManagement)
}

// EnrollURL returns the ms-device-enrollment deep link that starts MDM-only
// enrollment from a browser or the Run dialog.
func EnrollURL(publicURL, email string) string {
	return "ms-device-enrollment:?mode=mdm&username=" + url.QueryEscape(email) +
		"&servername=" + url.QueryEscape(publicURL+"/EnrollmentServer/Discovery.svc")
}

type soapHeader struct {
	Action    string `xml:"Header>Action"`
	MessageID string `xml:"Header>MessageID"`
}

func readSOAP(r *http.Request, v any) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return nil, err
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = true
	// Reject DTDs outright: SOAP never uses them, and refusing them rules out
	// entity expansion attacks regardless of the parser's behaviour.
	if bytes.Contains(body, []byte("<!DOCTYPE")) || bytes.Contains(body, []byte("<!ENTITY")) {
		return nil, errors.New("DTDs are not allowed")
	}
	return body, dec.Decode(v)
}

func writeSOAP(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	_, _ = io.WriteString(w, body)
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (d *Driver) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	var req soapHeader
	if _, err := readSOAP(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	writeSOAP(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing">
<s:Header>
<a:Action s:mustUnderstand="1">http://schemas.microsoft.com/windows/management/2012/01/enrollment/IDiscoveryService/DiscoverResponse</a:Action>
<a:RelatesTo>`+esc(req.MessageID)+`</a:RelatesTo>
</s:Header>
<s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<DiscoverResponse xmlns="http://schemas.microsoft.com/windows/management/2012/01/enrollment">
<DiscoverResult>
<AuthPolicy>OnPremise</AuthPolicy>
<EnrollmentVersion>4.0</EnrollmentVersion>
<EnrollmentPolicyServiceUrl>`+esc(d.PublicURL)+`/EnrollmentServer/Policy.svc</EnrollmentPolicyServiceUrl>
<EnrollmentServiceUrl>`+esc(d.PublicURL)+`/EnrollmentServer/Enrollment.svc</EnrollmentServiceUrl>
</DiscoverResult>
</DiscoverResponse>
</s:Body>
</s:Envelope>`)
}

func (d *Driver) handlePolicy(w http.ResponseWriter, r *http.Request) {
	var req soapHeader
	if _, err := readSOAP(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	// Credentials are validated at enrollment; the policy is not secret.
	writeSOAP(w, `<s:Envelope xmlns:a="http://www.w3.org/2005/08/addressing" xmlns:s="http://www.w3.org/2003/05/soap-envelope">
<s:Header>
<a:Action s:mustUnderstand="1">http://schemas.microsoft.com/windows/pki/2009/01/enrollmentpolicy/IPolicy/GetPoliciesResponse</a:Action>
<a:RelatesTo>`+esc(req.MessageID)+`</a:RelatesTo>
</s:Header>
<s:Body xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xmlns:xsd="http://www.w3.org/2001/XMLSchema">
<GetPoliciesResponse xmlns="http://schemas.microsoft.com/windows/pki/2009/01/enrollmentpolicy">
<response>
<policyID/>
<policyFriendlyName xsi:nil="true"/>
<nextUpdateHours xsi:nil="true"/>
<policiesNotChanged xsi:nil="true"/>
<policies>
<policy>
<policyOIDReference>0</policyOIDReference>
<cAs xsi:nil="true"/>
<attributes>
<commonName>VaanarSena</commonName>
<policySchema>3</policySchema>
<certificateValidity>
<validityPeriodSeconds>`+fmt.Sprint(int(pki.DeviceCertValidity.Seconds()))+`</validityPeriodSeconds>
<renewalPeriodSeconds>2592000</renewalPeriodSeconds>
</certificateValidity>
<permission><enroll>true</enroll><autoEnroll>false</autoEnroll></permission>
<privateKeyAttributes>
<minimalKeyLength>2048</minimalKeyLength>
<keySpec xsi:nil="true"/>
<keyUsageProperty xsi:nil="true"/>
<permissions xsi:nil="true"/>
<algorithmOIDReference xsi:nil="true"/>
<cryptoProviders xsi:nil="true"/>
</privateKeyAttributes>
<revision><majorRevision>101</majorRevision><minorRevision>0</minorRevision></revision>
<supersededPolicies xsi:nil="true"/>
<privateKeyFlags xsi:nil="true"/>
<subjectNameFlags xsi:nil="true"/>
<enrollmentFlags xsi:nil="true"/>
<generalFlags xsi:nil="true"/>
<hashAlgorithmOIDReference>0</hashAlgorithmOIDReference>
<rARequirements xsi:nil="true"/>
<keyArchivalAttributes xsi:nil="true"/>
<extensions xsi:nil="true"/>
</attributes>
</policy>
</policies>
</response>
<cAs xsi:nil="true"/>
<oIDs>
<oID>
<value>2.16.840.1.101.3.4.2.1</value>
<group>1</group>
<oIDReferenceID>0</oIDReferenceID>
<defaultName>szOID_NIST_sha256</defaultName>
</oID>
</oIDs>
</GetPoliciesResponse>
</s:Body>
</s:Envelope>`)
}

type enrollReq struct {
	MessageID string `xml:"Header>MessageID"`
	Username  string `xml:"Header>Security>UsernameToken>Username"`
	Password  string `xml:"Header>Security>UsernameToken>Password"`
	CSR       string `xml:"Body>RequestSecurityToken>BinarySecurityToken"`
	Context   []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:"Value"`
	} `xml:"Body>RequestSecurityToken>AdditionalContext>ContextItem"`
}

func (e *enrollReq) ctx(name string) string {
	for _, c := range e.Context {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

func soapFault(w http.ResponseWriter, relatesTo, code, msg string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing">
<s:Header><a:Action s:mustUnderstand="1">http://schemas.microsoft.com/windows/pki/2009/01/enrollment/RSTRC/wstepfault</a:Action>
<a:RelatesTo>`+esc(relatesTo)+`</a:RelatesTo></s:Header>
<s:Body><s:Fault><s:Code><s:Value>s:Receiver</s:Value><s:Subcode><s:Value xmlns:e="http://schemas.microsoft.com/windows/pki/2009/01/enrollment">e:`+code+`</s:Value></s:Subcode></s:Code>
<s:Reason><s:Text xml:lang="en-US">`+esc(msg)+`</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`)
}

func (d *Driver) handleEnrollment(w http.ResponseWriter, r *http.Request) {
	var req enrollReq
	if _, err := readSOAP(r, &req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	deviceID := req.ctx("DeviceID")
	if deviceID == "" || req.CSR == "" {
		soapFault(w, req.MessageID, "InvalidRequest", "missing DeviceID or certificate request")
		return
	}
	tok, err := d.Store.ConsumeEnrollmentToken(ctx, secrets.Hash(strings.TrimSpace(req.Password)), "windows")
	if err != nil {
		_ = d.Store.Audit(ctx, "device", "windows.enroll_denied", deviceID, map[string]any{"user": req.Username}, httpx.ClientIP(r))
		soapFault(w, req.MessageID, "AuthenticationFailure", "the enrollment token is invalid, expired or already used")
		return
	}
	if tok.Assignee != "" && !strings.EqualFold(tok.Assignee, req.Username) {
		soapFault(w, req.MessageID, "AuthorizationFailure", "this enrollment token is assigned to a different user")
		return
	}
	csr, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(req.CSR), ""))
	if err != nil {
		soapFault(w, req.MessageID, "InvalidRequest", "certificate request is not base64")
		return
	}
	cn := "vaanarsena-windows-" + sanitize(deviceID)
	cert, err := d.CA.SignCSR(csr, cn)
	if err != nil {
		soapFault(w, req.MessageID, "InvalidRequest", err.Error())
		return
	}
	// Device enrollment (EnrollmentType=Device) installs the identity in the
	// machine store; user enrollment (Full) in the user store.
	certStore := "User"
	if req.ctx("EnrollmentType") == "Device" {
		certStore = "System"
	}
	now := time.Now()
	assignee := tok.Assignee
	if assignee == "" {
		assignee = req.Username
	}
	dev, err := d.Store.UpsertDevice(ctx, &store.Device{
		Platform: store.PlatformWindows, Ownership: tok.Ownership, Status: store.StatusEnrolling,
		Name: req.ctx("DeviceName"), OSVersion: req.ctx("OSVersion"), Assignee: assignee,
		NativeID: deviceID, CertSerial: pki.SerialHex(cert), EnrollmentTokenID: &tok.ID, EnrolledAt: &now,
		PlatformIDs: []byte(fmt.Sprintf(`{"hwDevId":%q,"enrollmentType":%q,"certStore":%q}`,
			req.ctx("HWDevID"), req.ctx("EnrollmentType"), certStore)),
	})
	if err != nil {
		d.Log.Error("store windows device", "err", err)
		soapFault(w, req.MessageID, "InternalServiceFault", "internal error")
		return
	}
	mdm.AttachToGroup(ctx, d.Store, tok, dev.ID)
	_ = d.Store.Audit(ctx, "device", "device.enroll", dev.ID, map[string]any{"platform": "windows", "ownership": tok.Ownership, "user": req.Username}, httpx.ClientIP(r))

	prov := d.provisioningDoc(dev.ID, cn, certStore, base64.StdEncoding.EncodeToString(cert.Raw), pki.Thumbprint(cert))
	created := now.UTC().Format("2006-01-02T15:04:05.000Z")
	expires := now.Add(10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")
	writeSOAP(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:a="http://www.w3.org/2005/08/addressing" xmlns:u="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">
<s:Header>
<a:Action s:mustUnderstand="1">http://schemas.microsoft.com/windows/pki/2009/01/enrollment/RSTRC/wstep</a:Action>
<a:RelatesTo>`+esc(req.MessageID)+`</a:RelatesTo>
<o:Security s:mustUnderstand="1" xmlns:o="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
<u:Timestamp u:Id="_0"><u:Created>`+created+`</u:Created><u:Expires>`+expires+`</u:Expires></u:Timestamp>
</o:Security>
</s:Header>
<s:Body>
<RequestSecurityTokenResponseCollection xmlns="http://docs.oasis-open.org/ws-sx/ws-trust/200512">
<RequestSecurityTokenResponse>
<TokenType>http://schemas.microsoft.com/5.0.0.0/ConfigurationManager/Enrollment/DeviceEnrollmentToken</TokenType>
<DispositionMessage xmlns="http://schemas.microsoft.com/windows/pki/2009/01/enrollment"/>
<RequestedSecurityToken>
<BinarySecurityToken ValueType="http://schemas.microsoft.com/5.0.0.0/ConfigurationManager/Enrollment/DeviceEnrollmentProvisionDoc" EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd#base64binary" xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">`+
		base64.StdEncoding.EncodeToString([]byte(prov))+`</BinarySecurityToken>
</RequestedSecurityToken>
<RequestID xmlns="http://schemas.microsoft.com/windows/pki/2009/01/enrollment">0</RequestID>
</RequestSecurityTokenResponse>
</RequestSecurityTokenResponseCollection>
</s:Body>
</s:Envelope>`)
}

// provisioningDoc is the wap-provisioningdoc that installs the CA and identity
// and configures the OMA-DM client (w7 APPLICATION) and its poll schedule.
func (d *Driver) provisioningDoc(entID, cn, certStore, clientB64, clientThumb string) string {
	search := url.QueryEscape("Subject=CN=" + cn + "&Stores=MY\\" + certStore)
	// QueryEscape encodes spaces as '+', which Windows does not decode here.
	search = strings.ReplaceAll(search, "+", "%20")
	return `<wap-provisioningdoc version="1.1">
<characteristic type="CertificateStore">
<characteristic type="Root"><characteristic type="System">
<characteristic type="` + pki.Thumbprint(d.CA.Cert) + `"><parm name="EncodedCertificate" value="` + base64.StdEncoding.EncodeToString(d.CA.Cert.Raw) + `"/></characteristic>
</characteristic></characteristic>
<characteristic type="My"><characteristic type="` + certStore + `">
<characteristic type="` + clientThumb + `"><parm name="EncodedCertificate" value="` + clientB64 + `"/></characteristic>
<characteristic type="PrivateKeyContainer"/>
</characteristic></characteristic>
</characteristic>
<characteristic type="APPLICATION">
<parm name="APPID" value="w7"/>
<parm name="PROVIDER-ID" value="` + ProviderID + `"/>
<parm name="NAME" value="` + esc(d.Org) + `"/>
<parm name="ADDR" value="` + esc(d.PublicURL) + `/ManagementServer/MDM.svc"/>
<parm name="CONNRETRYFREQ" value="6"/>
<parm name="INITIALBACKOFFTIME" value="30000"/>
<parm name="MAXBACKOFFTIME" value="120000"/>
<parm name="BACKCOMPATRETRYDISABLED"/>
<parm name="DEFAULTENCODING" value="application/vnd.syncml.dm+xml"/>
<parm name="SSLCLIENTCERTSEARCHCRITERIA" value="` + esc(search) + `"/>
<characteristic type="APPAUTH">
<parm name="AAUTHLEVEL" value="CLIENT"/><parm name="AAUTHTYPE" value="DIGEST"/>
<parm name="AAUTHSECRET" value="unused"/><parm name="AAUTHDATA" value="AAAA"/>
</characteristic>
<characteristic type="APPAUTH">
<parm name="AAUTHLEVEL" value="APPSRV"/><parm name="AAUTHTYPE" value="DIGEST"/>
<parm name="AAUTHNAME" value="unused"/><parm name="AAUTHSECRET" value="unused"/><parm name="AAUTHDATA" value="AAAA"/>
</characteristic>
</characteristic>
<characteristic type="DMClient"><characteristic type="Provider"><characteristic type="` + ProviderID + `">
<parm name="EntDMID" value="` + esc(entID) + `"/>
<parm name="SignedEntDMID" value=""/>
<characteristic type="Poll">
<parm name="NumberOfFirstRetries" value="8" datatype="integer"/>
<parm name="IntervalForFirstSetOfRetries" value="1" datatype="integer"/>
<parm name="NumberOfSecondRetries" value="5" datatype="integer"/>
<parm name="IntervalForSecondSetOfRetries" value="5" datatype="integer"/>
<parm name="NumberOfRemainingScheduledRetries" value="0" datatype="integer"/>
<parm name="IntervalForRemainingScheduledRetries" value="15" datatype="integer"/>
<parm name="PollOnLogin" value="true" datatype="boolean"/>
</characteristic>
</characteristic></characteristic></characteristic>
</wap-provisioningdoc>`
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			b.WriteRune(r)
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}
