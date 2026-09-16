// Package kaleidoscoperpc implements KaleidoscopeRPC v1, a header-only RPC
// transport for net/http.
//
// The package intentionally keeps transport and business logic separate:
// Server parses and validates the wire protocol before invoking a Handler,
// and Client validates a complete response before returning it to callers.
// Business handlers never receive a ResponseWriter and therefore cannot
// accidentally violate the header-only transport contract.
package kaleidoscoperpc
