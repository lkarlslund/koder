// Package service is the only application-facing boundary for durable Memory.
//
// Callers such as tools, HTTP handlers, turn drivers, and background curators use Service
// rather than the underlying store. Service owns canonical validation, authorization and
// scope policy, actor/revision attribution, transaction selection, retrieval, and the
// decision metadata that makes mutations auditable. Store implementations own durability
// and derived indexes, but do not make user-facing policy decisions.
package service
