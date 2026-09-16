# kaleidoscoperpc

A standard-library-only Go implementation of the KaleidoscopeRPC v1
header-only transport, with a strict shared codec, `net/http` server/client,
resource ceilings, panic containment, deterministic encoding, property tests,
and native Go fuzz targets.

The package is deliberately transport-only. Procedure schemas and business types
stay above it.

## Requirements

* Go 1.23+ (the implementation itself only uses the standard library).
* An HTTP path where the body is not required.
* Header limits configured consistently across every proxy/server in the chain.

The repository currently uses the local module path `kaleidoscoperpc`. Change the
`module` line in `go.mod` to your canonical repository path before publishing it.

## Server integration

Keep business code behind `HandlerFunc`; it never receives an
`http.ResponseWriter`, so it cannot accidentally write a body or commit a partial
protocol response.

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    krpc "kaleidoscoperpc"
)

func main() {
    mux := krpc.NewMux()
    if err := mux.HandleFunc("echo", func(ctx context.Context, req *krpc.Request) (*krpc.Response, error) {
        message, ok := req.Arg("message")
        if !ok {
            out := krpc.NewResponse()
            _ = out.Set("error", "message is required")
            return nil, &krpc.StatusError{Status: http.StatusUnprocessableEntity, Response: out}
        }

        out := krpc.NewResponse()
        if err := out.Set("message", message); err != nil {
            return nil, err
        }
        return out, nil
    }); err != nil {
        log.Fatal(err)
    }

    rpc, err := krpc.NewServer(krpc.ServerConfig{
        Mux: mux,
        OnError: func(ctx context.Context, err error) {
            log.Printf("kaleidoscope: %v", err)
        },
    })
    if err != nil {
        log.Fatal(err)
    }

    // Prefer a dedicated listener/virtual host and use rpc directly as Handler.
    // net/http.ServeMux may clean/redirect malformed paths before rpc sees them.
    srv := &http.Server{
        Addr:              ":8080",
        Handler:           rpc,
        ReadHeaderTimeout: 5 * time.Second,
        WriteTimeout:      10 * time.Second,
        MaxHeaderBytes:    1 << 20,
    }
    log.Fatal(srv.ListenAndServe())
}
```

Important deployment point: package limits apply **after** `net/http` has parsed
the request headers. Set `http.Server.MaxHeaderBytes` and `ReadHeaderTimeout`, and
configure equivalent limits/timeouts in NGINX, Envoy, ingress, CDN, or other
proxies. Newer Go releases also expose a request header-value-count ceiling; use
it when your selected Go version supports it.

### Passing infrastructure metadata to business logic

Use ordinary HTTP middleware to authenticate, trace, or add values to
`request.Context()` before the RPC handler. Business handlers consume the context
and validated `*krpc.Request`; they do not need raw RPC fields.

## Client integration

```go
client, err := krpc.NewClient(krpc.ClientConfig{
    BaseURL:   "https://rpc.example.com/",
    Placement: krpc.FunctionInBoth,
})
if err != nil {
    log.Fatal(err)
}

args := krpc.NewArgs()
_ = args.Set("message", "hello")
_ = args.SetBytes("payload", arbitraryBytes)

resp, err := client.Call(ctx, "echo", args)
if err != nil {
    // Transport or protocol failure.
    return err
}
if !resp.OK() {
    // A syntactically valid application/routing response; inspect StatusCode
    // and its Kaleidoscope arguments.
}

message, _ := resp.Arg("message")
payload, _ := resp.Bytes("payload")
```

The high-level client:

* sends a nil body;
* disables redirects by default to avoid replaying argument fields to another
  endpoint;
* clones a supplied `*http.Client` rather than mutating it;
* caps response-header bytes when the underlying transport is `*http.Transport`;
* validates the complete protocol response before returning it;
* returns valid non-2xx protocol responses normally so business code can inspect
  their status/arguments.

For a custom `RoundTripper`, configure its own response-header ceiling: a codec
limit cannot reclaim memory already consumed by a transport.

## Integrating with an existing `net/http` client

Use `EncodeRequest` when you own request construction but want to keep your
existing transport, tracing, retries, or middleware:

```go
req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://rpc.example.com/", nil)
if err != nil { /* ... */ }

args := krpc.NewArgs()
_ = args.Set("answer", "42")

err = krpc.EncodeRequest(req, "lookup", args, krpc.EncodeOptions{
    Placement: krpc.FunctionInHeader,
})
if err != nil { /* ... */ }

httpResp, err := myHTTPClient.Do(req)
if err != nil { /* ... */ }
defer httpResp.Body.Close()

rpcResp, err := krpc.DecodeResponse(httpResp, krpc.Limits{})
```

`EncodeRequest` owns and replaces only the KaleidoscopeRPC header namespace; it
leaves unrelated HTTP fields intact.

## Text vs bytes

`Args.Set` is for canonical text field values. It rejects controls, leading or
trailing HTTP whitespace, and commas. Empty text is valid.

`Args.SetBytes` is the lossless path for arbitrary data. It uses strict Base85
and automatically combines `binary` with `reassemble` when the encoded payload
needs multiple fields. Empty binary payloads are valid too.

On input:

* `Arg(name)` returns the logical wire text. For binary this is Base85 text.
* `Bytes(name)` returns decoded bytes for binary values and UTF-8 bytes for text.
* `IsBinary(name)` lets a schema layer enforce which representation it expects.

## Limits

`Limits{}` means safe package defaults:

```text
MaxArguments             128
MaxDirectives            256
MaxFragmentsPerArgument  1024
MaxArgumentNameBytes     128
MaxFunctionNameBytes     128
MaxProtocolHeaderFields  2048
MaxHeaderValueBytes      8192
MaxServiceBytes          8192
MaxTotalWireBytes        1 MiB
MaxDecodedBytes          1 MiB
```

These are defensive ceilings, not promises that every proxy will accept that
much. Size your package and infrastructure limits to the **smallest** component
in the request path.

## Testing

```bash
make check            # vet + unit/integration + race
make cover            # statement coverage
make fuzz             # all native fuzz targets, 10s each by default
FUZZTIME=1m make fuzz # longer local/CI campaign
make bench
```

Fuzz targets cover Base85 round-trip/canonical decoding, service grammar,
malformed request construction, full binary message round-trip, and text-value
validation. CI also runs vet, normal tests, race tests, and fuzz smoke tests.

## Protocol and HTTP caveats

* Fragmentation solves a single-field size limit, not the aggregate HTTP header
  limit.
* The protocol does not authenticate peers. Use TLS and an auth layer.
* Do not use unquoted RPC values directly in shell, regex, SQL, or other language
  contexts; context-specific escaping remains required.
* A response with `Content-Length > 0` or transfer encoding is rejected. A peer
  that sends an undeclared HTTP/2/EOF-framed body cannot be proven absent from
  headers alone without consuming the body; the client deliberately does not
  consume bodies and closes them instead.
* If a proxy rewrites paths, prefer `FunctionInHeader` or `FunctionInBoth` and
  ensure the request arriving at the RPC handler still has path `/` or the exact
  function path.

See `SPEC.md` for the wire contract.