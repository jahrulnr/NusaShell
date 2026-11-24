; C# outline query — complete replacement of the stock v0.58.0 query.
; Adds record_declaration (kept as kind "record"), struct_declaration,
; and both block-scoped and file-scoped namespace declarations as modules.
(method_declaration (identifier) @name) @definition.method
(class_declaration (identifier) @name) @definition.class
(interface_declaration (identifier) @name) @definition.interface
(record_declaration (identifier) @name) @definition.record
(struct_declaration (identifier) @name) @definition.struct
(namespace_declaration name: (_) @name) @definition.module
(file_scoped_namespace_declaration name: (_) @name) @definition.module
(enum_declaration (identifier) @name) @definition.type
(constructor_declaration (identifier) @name) @definition.constructor
