# Protobuf annotation syntax

Annotations add documentation metadata and examples to real protobuf comments. They do not change the protobuf descriptor or wire format. Use `lint -strict-annotations` to check the forms below; the site build also checks protobuf annotations strictly.

Only comment text is scanned. Marker-like text in a protobuf string literal or option value is not an annotation. File metadata can appear in ordinary line or block comments. Put declaration-specific annotations in the documentation comment attached to that declaration.

## Accepted directives

| Scope | Form | Meaning |
| --- | --- | --- |
| File | `@title: Payments API` | Sets the file's documentation title. |
| File sections | `@header: Requests` | Groups following message definitions under a section. |
| File | `@ignore-file` | Omits the file from generated documentation. |
| Message | `@autocode[json]` or `@autocode[xml]` | Generates an example from the message and its field metadata. |
| Message | `@code[json]:` or `@code[xml]:` | Starts a fixed, validated example. |
| Message, enum, field, enum value | `@ignore` | Omits the annotated definition. |
| Field | `@doc: min=1 max=10` | Supplies structured example metadata. |
| Field | `@min=1`, `@max=10`, `@len=32`, `@val="sample text"`, `@type=email` | Legacy field metadata. |

Names are case-sensitive. Use the spellings shown. A known marker can follow prose on the same comment line: `// Private implementation detail. @ignore` still hides the declaration. To discuss a directive as ordinary text, quote it or use a code span or literal block.

`@doc` accepts an optional colon, so `@doc min=1` and `@doc: min=1` are equivalent. It consumes the rest of its line as whitespace-separated attributes and bare flags. Spaces around `=` are allowed within `@doc`. Legacy attributes require `=` immediately after the marker, as in `@min=1`.

```proto
// @title: Orders API
// @header: Requests

// Creates an order.
// @autocode[json]
message CreateOrder {
  // Quantity of items.
  // @doc: min=1 max=20 example=3
  int32 quantity = 1;

  // Customer address used in generated examples.
  // @doc: type=email
  string email = 2;

  // @doc: ignore
  string internal_note = 3;
}
```

The structured field attributes are:

| Attribute | Accepted aliases | Meaning |
| --- | --- | --- |
| `min` | none | Inclusive numeric lower bound. |
| `max` | none | Inclusive numeric upper bound. |
| `max_len` | `max-length`, `len` | Non-negative decimal integer maximum string length or collection size for examples. |
| `example` | `val` | Explicit typed example value, taking precedence over random sampling. |
| `type` | none | Selects an existing example-generation category. |

`ignore` is a bare `@doc` flag, not an `ignore=true` attribute. The accepted `type` values are `int`, `uint`, `float`, `bool`, `string`, `enum`, `jwt`, `uuid`, `struct`, `email`, `phone`, and `password`; these values are case-insensitive. A type hint does not declare a new protobuf type. An explicit example must be appropriate for its selected type; it is not a request to generate or validate a complete application payload.

Comma-separated attributes, `min: 1`, `@doc { ... }`, and attribute names outside this table are not supported forms. Do not repeat an attribute, use both aliases for the same attribute, or mix a legacy marker with its structured equivalent for one field. Strict mode reports duplicate, unknown, and misplaced annotations. Duplicate keys inside one `@doc` are syntax errors even without strict mode. In relaxed mode, unsupported names can be ignored; they do not acquire new semantics. Ordinary inline mentions of unknown `@names` in prose remain text, while strict mode rejects unknown standalone directives.

## Values, quotes, and literal examples

An unquoted attribute value ends at whitespace. Quote values containing spaces, quotes, or literal marker text.

```proto
// @doc: example="A quoted \"word\" and a backslash \\"
string label = 1;

// @doc: example='It\'s a literal \n sequence'
string note = 2;
```

Double quotes use Go interpreted-string escapes, including `\"`, `\\`, `\n`, `\t`, `\xNN`, `\uNNNN`, and `\UNNNNNNNN`. Single quotes decode `\'` and `\\`; other backslash sequences, such as `\n`, remain literal characters. A quoted value must close on the same comment line. Missing values, unterminated quotes, and invalid double-quoted escapes are errors.

Apostrophes inside words such as `user's` are ordinary prose, not the start of a quoted span. Markers inside quoted text, matching backtick code spans, fenced Markdown examples, or four-column indented code are literal text. Fences use at least three backticks or tildes and close with the same character and at least the opening width. For raw protobuf comments, the conventional single space after `//` or a block-comment `*` is removed before counting code indentation.

````proto
// How to hide a field: `@ignore`.
//
// ```proto
// @ignore
// string private_value = 1;
// ```
//
//     @ignore
message DocumentedExample {}
````

Place other annotations before an example directive. The first real `@code` or `@autocode` ends annotation scanning for that comment; later marker text belongs to the example area. This prevents `@ignore`, `@title`, or `@autocode` inside JSON, XML, or other example text from becoming metadata. Independent detached, leading, and trailing comment chunks retain their own boundaries.

A fixed example can occupy the rest of the marker line or following lines of the same comment. JSON is the default for bare `@code`; the explicit bracketed spelling is clearer. `@autocode` requires `[json]` or `[xml]`. An optional colon can follow the closing bracket.

```proto
// A fixed response example.
// @code[json]:
// {"message": "Literal @ignore text", "count": 2}
message Response {}
```

Fixed JSON must be a valid JSON value. Fixed XML must be a well-formed document with one root element. The payload is not another layer of annotation syntax. Use one example directive per message; named example sets and additional output formats are not implemented.

## Exact inclusive numeric bounds

Integer constraints are parsed exactly, including integers beyond `2^53`. Decimal values, exponent notation, hexadecimal floating-point notation, and underscore separators are accepted where Go's numeric parser accepts them. Values must be finite; compatibility parsing rejects overflow, `NaN`, and infinities.

For integer fields, the generator uses the inclusive interval `ceil(min)` through `floor(max)`. Both normalized endpoints must fit the field's signed or unsigned 32-bit or 64-bit domain. Empty intervals fail with a located validation error.

```proto
// @doc: min=9007199254740993 max=9007199254740993
int64 exact_id = 1; // Always generates 9007199254740993.

// @doc: min=1.2 max=2.8
int32 count = 2; // Only the integer 2 is allowed.

// @doc: min=0 max=18446744073709551615
uint64 full_range = 3;
```

`min=1.2 max=1.8` contains no integer and fails. Equal integral bounds produce a fixed example. Full `int64` bounds are `-9223372036854775808` through `9223372036854775807`; full `uint64` bounds are `0` through `18446744073709551615`. Exact source literals also appear in generated constraint tables.

Each numeric bound is limited to 256 bytes and an absolute decimal or binary exponent of 1024. This is an allocation limit, not a promise that every value within it fits a protobuf domain or finite compatibility float. Floating-point examples use their normal IEEE-754 representation; exact integer arithmetic does not make decimal floating-point samples exact.

Without numeric bounds, integer examples use a small inclusive range from 0 to 1,000,000. One missing endpoint keeps that default when possible; if an explicit endpoint lies beyond it, the missing endpoint moves to the explicit one. Bounds constrain generated samples. An explicit `example` or `@val` takes precedence, so supply a value consistent with the documented constraints. Example generation also has depth, field, and collection budgets; `max_len` is a maximum, not a request to bypass those limits.

## Visibility and future syntax

`@ignore` hides annotated messages, enums, fields, and enum values. Ignoring a container hides its children. Typed API exports and preserved protobuf descriptor documents also apply this visibility rule. References from visible definitions to ignored types remain opaque; they do not expose hidden members or schema links. The source `.proto` and its runtime wire behavior are unchanged.

Only the directives in the accepted table are implemented. Names such as `@page`, `@request`, and `@response`, nested directive languages, and arbitrary new `@doc` attributes are proposals or unsupported text, not aliases for current behavior. Markdown pages, project manifests, and imported OpenAPI contracts use their own [portable format](format.md); they do not add protobuf comment directives implicitly.
