# KaleidoscopeRPC v1 — production wire profile

This file is the authoritative wire contract implemented by this module.

## 1. Purpose and scope

KaleidoscopeRPC is a bodyless RPC transport over HTTP. It carries only:

- a procedure name;
- named arguments;
- wire directives describing fragmentation and binary encoding.

It intentionally has no schema, positional arguments, application types,
authentication model, or business semantics.

The protocol can carry arbitrary **content**, but not unbounded size. Every HTTP
implementation and intermediary has an aggregate header-block limit. Fragmentation
only removes the single-field-value bottleneck; it cannot bypass the aggregate
limit of the HTTP chain.

## 2. HTTP transport

- Any HTTP method is allowed and has no protocol-level meaning.
- The request path is exactly `/` or `/<function>`.
- Query strings are not part of v1 and are rejected.
- A function in the path is literal ASCII and MUST NOT be percent-encoded.
- Request bodies are absent. Receivers never read a body and reject body framing
  (`Content-Length != 0` or transfer encoding).
- Response bodies are absent.
- All payload is in the request target and HTTP fields.

## 3. Version

Every request and response contains exactly one logical field:

```text
X-Kaleidoscope: v1
```

Missing, duplicated (when visible to the HTTP API), or different values are a
protocol error. An implementation generating an error response emits the version
it implements (`v1`); it does not echo an unsupported version token.

## 4. Function name

The function is present exactly once logically, by path, field, or both:

```text
/<function>
X-KaleidoscopeFn: <function>
```

If both forms are present, values are byte-for-byte equal. Grammar:

```text
[A-Za-z0-9_]+
```

The function name is case-sensitive. `/` without `X-KaleidoscopeFn` is invalid.
The response never contains `X-KaleidoscopeFn`.

## 5. Argument names and fields

Arguments are named and encoded as:

```text
X-Kaleidoscope-<Name>
```

The canonical protocol name grammar is:

```text
[a-z0-9]+(?:_[a-z0-9]+)*
```

Header mapping is reversible:

- `_` becomes `-`;
- the first ASCII letter of every segment is upper-cased by the sender;
- receivers treat the field name case-insensitively and recover a lower-case
  protocol name.

Examples:

```text
answer            -> X-Kaleidoscope-Answer
data_blob         -> X-Kaleidoscope-Data-Blob
user_information  -> X-Kaleidoscope-User-Information
```

CamelCase argument names are intentionally not supported: HTTP field-name
case-folding makes them non-recoverable.

Each physical argument/fragment field is singleton. Multiple values visible to
`net/http` are an error.

### 5.1 Text values

A plain text argument is a valid UTF-8 HTTP field value with these additional
canonicality rules:

- empty is valid and distinct from absent;
- C0 controls and DEL are forbidden;
- leading/trailing SP or HTAB are forbidden because HTTP parsing can trim them;
- comma is forbidden so an intermediary cannot merge duplicated singleton fields
  into a value that looks like a legitimate argument.

Use `binary(name)` for arbitrary octets, control bytes, edge whitespace, commas,
or whenever byte-for-byte portability through heterogeneous intermediaries is
more important than a human-readable field value.

## 6. Service directives

Optional singleton field:

```text
X-KaleidoscopeService: <directives>
```

Grammar:

```text
directives := ws? directive (ws? "," ws? directive)* ws?
directive  := word "(" ws? [ word (ws? "," ws? word)* ] ws? ")"
word       := [A-Za-z0-9_]+
ws         := " " | HTAB
```

v1 knows exactly two directive names:

```text
reassemble(name, N)
binary(name)
```

Unknown directives, malformed syntax, duplicate same-kind directives, or
inconsistent field reservations are protocol errors.

`reassemble(name,N)` and `binary(name)` MAY coexist for the same logical
argument. Their order is fixed and not ambiguous:

1. reassemble the textual wire representation;
2. Base85-decode it.

This is necessary for binary payloads larger than one practical HTTP field.

## 7. `reassemble`

```text
reassemble(user_information, 3)
```

means the logical argument is carried by exactly:

```text
X-Kaleidoscope-User-Information1
X-Kaleidoscope-User-Information2
X-Kaleidoscope-User-Information3
```

Rules:

- `N` is canonical decimal, no leading zeroes, range `1..1024`;
- all declared fragments are present exactly once;
- the bare field is absent;
- numeric-suffix fragment fields for the same base outside `1..N` are errors;
- a leading-zero fragment number is non-canonical and invalid;
- nonnumeric suffixes (for example `-Extra`) are independent arguments;
- directive field reservations for different logical arguments must not collide.

Fragments concatenate in numeric order with no separator.

For text arguments, every physical fragment itself must remain a canonical text
field value; a sender therefore chooses boundaries that do not expose whitespace
at a fragment edge. If no such boundary exists, the value must be sent as binary.

## 8. `binary` and Base85

```text
binary(payload)
```

marks the logical wire text as Base85. `Arg(name)` exposes the reassembled Base85
text and `Bytes(name)` exposes decoded bytes. Empty binary values are valid.

### 8.1 Alphabet

v1 uses the 85-character RFC 1924 alphabet, in this order:

```text
0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz!#$%&()*+-;<=>?@^_`{|}~
```

It consists entirely of visible US-ASCII characters and notably omits comma,
quotes, slash, colon, square brackets, and backslash. RFC 1924 chose the same set
from the 94 printable ASCII characters.

There is **no `z` shorthand**. `z` is an ordinary Base85 digit.

### 8.2 Encoding

Input is processed in 4-byte big-endian groups:

1. interpret 4 bytes as an unsigned 32-bit integer;
2. emit exactly 5 base-85 digits, most significant first;
3. for a final 1/2/3-byte input group, zero-pad to 4 bytes and emit only
   2/3/4 leading Base85 digits respectively.

Empty input encodes to the empty string.

### 8.3 Strict decoding

- only alphabet characters are accepted;
- a one-character final group is invalid;
- 5 digits evaluating above `2^32-1` are invalid;
- a 2/3/4-character final group is padded internally with digit 84 to recover
  1/2/3 bytes, then re-encoded and compared with the original tail;
- non-canonical partial encodings are rejected.

Canonical decoding matters for signatures, caches, and equality checks: one byte
sequence has exactly one accepted textual representation.

## 9. Responses

Responses use the same version, argument, fragmentation, and binary machinery.
They do not contain a function field and have no body.

Defined transport statuses:

- `200`: successfully handled call;
- `400`: malformed KaleidoscopeRPC request.

Other statuses are allowed for application/routing semantics. This module uses
`404` for an unregistered function and `500` for unhandled application failures
or recovered panics. A valid protocol response at any status still carries
`X-Kaleidoscope: v1`.

## 10. Resource limits

Resource ceilings are implementation policy rather than wire syntax. This module
applies limits before business logic to:

- logical arguments;
- directives;
- fragments per argument;
- function and argument-name bytes;
- protocol field count;
- single field-value bytes;
- service-field bytes;
- aggregate Kaleidoscope wire bytes;
- aggregate decoded binary bytes.

Deployments MUST also configure limits in the HTTP server/proxies because those
layers parse headers before the package handler runs.

## 11. Security properties and non-goals

The protocol parser is strict and rejects malformed/ambiguous encodings. It does
not provide authentication, confidentiality, replay protection, authorization,
or application schemas. Use TLS and application/infrastructure authentication as
appropriate. Do not interpolate field values into shell commands or regular
expressions without context-specific escaping; no 85-character alphabet can make
all printable data universally safe in all unrelated languages.
