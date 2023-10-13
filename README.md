[![Go Report Card](https://goreportcard.com/badge/github.com/kordax/pb-md5-generator)](https://goreportcard.com/report/github.com/kordax/pb-md5-generator)


# Introduction

Markdown documentation generator for proto3 protocol.
This project consists of a custom program designed to parse Protocol Buffers (protobuf) definition files, specifically
focusing on extracting documentation annotations present in the comments. These annotations follow a custom doc
protocol, facilitating the generation of MD5 documentation from the protobuf files.

## Syntax

Comments in the protobuf files use a special syntax to annotate documentation. Here is an outline of the annotation
syntax:

1. **Description Comments**
   - Placed before message, enum, or service definitions.
   - Describes the purpose and usage of the subsequent element.
   - Example:
     ```protobuf
     /*
      * Description of the message or enum.
      */
     message ExampleMessage {}
     ```

2. **Code Block Annotations**
   - Utilize `@code[json]` to denote a JSON representation.
   - Placed within multiline comments.
   - Example:
     ```protobuf
     /*
      * @code[json]:
      * {
      *   "exampleField": "value"
      * }
      */
     message ExampleMessage {
       string exampleField = 1;
     }
     ```

3. **Autocode Annotations**
   - Use `@autocode[json]` to automatically generate code examples.
   - Placed within multiline comments.
   - No example content is provided within the comment.
   - Example:
     ```protobuf
     /*
      * @autocode[json]
      */
     message AutoCodeExample {}
     ```

4. **Header Annotations**
   - Denoted by `@header:`.
   - Describes a section or group of related elements. Will divide markdown document using a divide line.
   - Example:
     ```protobuf
     // @header: user attributes related stuff.
     ```

5. **Ignore Annotations**
   - Marked by `@ignore`.
   - Indicates that the following definition should be ignored by the parser.
   - Example:
     ```protobuf
     /*
      * @Ignore
      */
     message IgnoredMessage {}
     ```

## Extended Comment Annotations

In addition to the previously mentioned comment annotations, the program also supports the following annotations for
enhancing the detail and constraints in the protobuf file documentation:

1. **Max Annotation**
   - Syntax: `@max=<value>`
   - Sets the maximum value for numerical fields.
   - Example:
     ```protobuf
     message ExampleMessage {
       float exampleField = 1; // @max=1.6
     }
     ```

2. **Min Annotation**
   - Syntax: `@min=<value>`
   - Defines the minimum value for numerical fields.
   - Example:
     ```protobuf
     message ExampleMessage {
       float exampleField = 1; // @min=1.9
     }
     ```

3. **Len Annotation**
   - Syntax: `@len=<value>`
   - Sets the maximum string length. Applicable only to string fields.
   - Example:
     ```protobuf
     message ExampleMessage {
       string exampleField = 1; // @len=255
     }
     ```

4. **(AutoCode only!) Val Annotation**
   - Syntax: `@val=<value>`
   - Does not generate value automatically but uses the provided value for this tag.
   - Example:
     ```protobuf
     message ExampleMessage {
       string exampleField = 1; // @val="myCuStomVal12345"
     }
     ```

5. **Type Annotation**
   - Syntax: `@type=<value>`
   - Defines a custom type from the following
     list: `int`, `uint`, `float`, `bool`, `string`, `enum`, `jwt`, `uuid`, `email`, `phone`, `password`.
   - Example:
     ```protobuf
     message ExampleMessage {
       string emailField = 1; // @type=email
     }
     ```

You can combine all these annotations with field descriptions:

  ```protobuf
  message ExampleMessage {
    string emailField = 1; // that is my custom email field @type=email
  }
  ```

These extended annotations provide additional context and constraints for the fields in the protobuf definitions, aiding
in the generation of more detailed and accurate documentation.

## Program Usage

1. **Parsing Protobuf Files**
   - Run the program specifying the path to the `.proto` file as an argument.
   - The program will parse the file, extracting annotations and associated definitions.

2. **Generating MD5 Documentation**
   - Upon successful parsing, the program generates MD5 documentation.
   - The documentation is based on the annotated comments and the protobuf elements they describe.

3. **Output**
   - The generated documentation is either displayed to the user, saved to a file, or both, depending on the program
     configuration and user preferences.

### Example usage
`go get -u github.com/kordax/pb-md5-generator`

```console
pb-md5-generator -d protobufs/my-project/ -o ./README.md -p ./my-prefix-doc.md
```

There's a `test_protofile` in `internal/test-proto` directory for you to check out.

# Libraries Used in the Project

This document lists the libraries used in the project that are licensed under the MIT License, in accordance with their respective licenses.

# MIT Licensed Libraries

- **[golang/protobuf](https://github.com/golang/protobuf)** v1.5.0
    - **License**: [BSD-3 License](https://github.com/golang/protobuf/blob/master/LICENSE)

- **[google/uuid](https://github.com/google/uuid)** v1.3.0
    - **License**: [BSD-3 License](https://github.com/google/uuid/blob/master/LICENSE)

- **[goombaio/namegenerator](https://github.com/goombaio/namegenerator)** v0.0.0-20181006234301-989e774b106e
    - **License**: [Apache-2.0 License](https://github.com/goombaio/namegenerator/blob/master/LICENSE)

- **[pseudomuto/protokit](https://github.com/pseudomuto/protokit)** v0.2.1
    - **License**: [MIT License](https://github.com/pseudomuto/protokit/blob/master/LICENSE)

- **[rs/zerolog](https://github.com/rs/zerolog)** v1.29.1
    - **License**: [MIT License](https://github.com/rs/zerolog/blob/master/LICENSE)

- **[stretchr/testify](https://github.com/stretchr/testify)** v1.8.4
    - **License**: [MIT License](https://github.com/stretchr/testify/blob/master/LICENSE)

- **[mattn/go-colorable](https://github.com/mattn/go-colorable)** v0.1.13
    - **License**: [MIT License](https://github.com/mattn/go-colorable/blob/master/LICENSE)

- **[mattn/go-isatty](https://github.com/mattn/go-isatty)** v0.0.19
    - **License**: [MIT License](https://github.com/mattn/go-isatty/blob/master/LICENSE)

- **[pmezard/go-difflib](https://github.com/pmezard/go-difflib)** v1.0.0
    - **License**: [MIT License](https://github.com/pmezard/go-difflib/blob/master/LICENSE)

Note: The above links and license information are for illustrative purposes and may not be accurate. Please verify the license information by visiting the respective library's repository and reviewing their license file.
