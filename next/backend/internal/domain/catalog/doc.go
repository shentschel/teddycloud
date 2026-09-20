// Package catalog models editorial content and immutable playable revisions.
//
// Internal aggregate IDs are the only identity. Product/model/article metadata
// and audio fingerprints are typed external facts. Exact fingerprint matching
// is explicit and may still be ambiguous; import or lookup order is never a
// tie-breaker. This package contains no persistence, transport, filesystem, or
// third-party dependencies.
package catalog
