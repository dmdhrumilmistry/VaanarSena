# One agent check-in over mutual TLS: checkin.py <device dir> <json body>.
# Run from test/dev/.run (seed.sh does); VS_DEV_BASE overrides the server.
import json, os, ssl, sys, urllib.request
d, body = sys.argv[1], sys.argv[2]
ctx = ssl.create_default_context(cafile="tls.crt")
ctx.load_cert_chain(d + "/cert.pem", d + "/key.pem")
req = urllib.request.Request(os.environ.get("VS_DEV_BASE", "https://localhost:18443") + "/agent/v1/checkin", data=body.encode(), headers={"Content-Type": "application/json"}, method="POST")
with urllib.request.urlopen(req, context=ctx) as r:
    r.read()
