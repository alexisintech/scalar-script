// Package serialize provides the means to serialize models to JSON payloads.
// These payloads are typically used as responses of our APIs.
//
// There are two kind of types exported by this package: structs and functions.
// The structs are the responses meant to be serialized by users of this package.
// The functions are responsible for building those structs.
//
// Each type of this package maps to a particular model and is named after it.
// For example, the following function is responsible for constructing the
// serializable struct for a model.Integration:
//
//	func Integration(*model.Integration) IntegrationResponse
//
// All names in this package adhere to the following conventions.
//
// The name starts with the name of the corresponding model.
//
// If the type is a struct (i.e. the response to be returned) the suffix
// "Response" follows:
//
//	type IntegrationResponse struct {}
//
// If the type is a function, the name is identical to the model:
//
//	func Integration(*model.Integration) IntegrationResponse
//
// If serialization has to be differentiated depending on where the response
// is used for, then one of the suffixes are appended in the name of the type:
// "ToClientAPI", "ToServerAPI" or "ToDashboardAPI":
//
//	type IntegrationToClientAPIResponse struct {}
//	func IntegrationToClientAPI(i *model.Integration) IntegrationToClientAPIResponse
//
//	type IntegrationToServerAPIResponse struct {}
//	func IntegrationToServerAPI(i *model.Integration) IntegrationToServerAPIResponse
//
// This package should NOT depend on the database. Instead, all needed data
// should be passed to the functions.
package serialize
