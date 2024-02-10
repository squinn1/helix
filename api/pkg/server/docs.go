// Package server Code generated by swaggo/swag. DO NOT EDIT
package server

import "github.com/swaggo/swag"

const docTemplate = `{
    "schemes": {{ marshal .Schemes }},
    "swagger": "2.0",
    "info": {
        "description": "{{escape .Description}}",
        "title": "{{.Title}}",
        "contact": {
            "name": "Helix support",
            "url": "https://app.tryhelix.ai/",
            "email": "info@helix.ml"
        },
        "version": "{{.Version}}",
        "x-logo": {
            "altText": "Helix logo",
            "url": "https://avatars.githubusercontent.com/u/149581110?s=200\u0026v=4"
        }
    },
    "host": "{{.Host}}",
    "basePath": "{{.BasePath}}",
    "paths": {
        "/api/v1/tools": {
            "get": {
                "security": [
                    {
                        "BearerAuth": []
                    }
                ],
                "responses": {
                    "200": {
                        "description": "OK",
                        "schema": {
                            "$ref": "#/definitions/types.Tool"
                        }
                    }
                }
            }
        }
    },
    "definitions": {
        "types.OwnerType": {
            "type": "string",
            "enum": [
                "user"
            ],
            "x-enum-varnames": [
                "OwnerTypeUser"
            ]
        },
        "types.Tool": {
            "type": "object",
            "properties": {
                "config": {
                    "description": "TODO: tool configuration\nsuch as OpenAPI spec, function code, etc.",
                    "allOf": [
                        {
                            "$ref": "#/definitions/types.ToolConfig"
                        }
                    ]
                },
                "created": {
                    "type": "string"
                },
                "description": {
                    "type": "string"
                },
                "id": {
                    "type": "string"
                },
                "name": {
                    "type": "string"
                },
                "owner": {
                    "description": "uuid of owner entity",
                    "type": "string"
                },
                "owner_type": {
                    "description": "e.g. user, system, org",
                    "allOf": [
                        {
                            "$ref": "#/definitions/types.OwnerType"
                        }
                    ]
                },
                "tool_type": {
                    "$ref": "#/definitions/types.ToolType"
                },
                "updated": {
                    "type": "string"
                }
            }
        },
        "types.ToolApiAction": {
            "type": "object",
            "properties": {
                "description": {
                    "type": "string"
                },
                "method": {
                    "type": "string"
                },
                "name": {
                    "type": "string"
                },
                "path": {
                    "type": "string"
                }
            }
        },
        "types.ToolApiConfig": {
            "type": "object",
            "properties": {
                "actions": {
                    "description": "Read-only, parsed from schema on creation",
                    "type": "array",
                    "items": {
                        "$ref": "#/definitions/types.ToolApiAction"
                    }
                },
                "headers": {
                    "description": "Headers (authentication, etc)",
                    "type": "object",
                    "additionalProperties": {
                        "type": "string"
                    }
                },
                "query": {
                    "description": "Query parameters that will be always set",
                    "type": "object",
                    "additionalProperties": {
                        "type": "string"
                    }
                },
                "schema": {
                    "type": "string"
                },
                "url": {
                    "description": "Server override",
                    "type": "string"
                }
            }
        },
        "types.ToolConfig": {
            "type": "object",
            "properties": {
                "api": {
                    "$ref": "#/definitions/types.ToolApiConfig"
                }
            }
        },
        "types.ToolType": {
            "type": "string",
            "enum": [
                "api",
                "function"
            ],
            "x-enum-varnames": [
                "ToolTypeAPI",
                "ToolTypeFunction"
            ]
        }
    }
}`

// SwaggerInfo holds exported Swagger Info so clients can modify it
var SwaggerInfo = &swag.Spec{
	Version:          "0.1",
	Host:             "app.tryhelix.ai",
	BasePath:         "",
	Schemes:          []string{"https"},
	Title:            "HelixML API reference",
	Description:      "This is a HelixML AI API.",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  docTemplate,
	LeftDelim:        "{{",
	RightDelim:       "}}",
}

func init() {
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}
