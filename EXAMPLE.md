# Example Output

The following file shows a real generated markdown section (excerpt) for
`testdata/test-proto/test.pb.desc`.

```markdown
* Table Of Contents
     * [ClientRequest](#doc_generator_test.ClientRequest)
     * [ServerResponse](#doc_generator_test.ServerResponse)
     * [TokenRequest](#doc_generator_test.TokenRequest)
     * [TokenResponse](#doc_generator_test.TokenResponse)
     * [RegistrationRequest](#doc_generator_test.RegistrationRequest)
     * [RegistrationResponse](#doc_generator_test.RegistrationResponse)
     * [ListServersRequest](#doc_generator_test.ListServersRequest)
     * [ListServersResponse](#doc_generator_test.ListServersResponse)
     * [ServerEntry](#doc_generator_test.ServerEntry)
     * [ServerPlan](#doc_generator_test.ServerPlan)

* Enums
     * [LoginStatus](#doc_generator_test.LoginStatus)
     * [RegistrationStatus](#doc_generator_test.RegistrationStatus)
     * [InstanceStatus](#doc_generator_test.InstanceStatus)
     * [ListServersStatus](#doc_generator_test.ListServersStatus)
# test_proto

## API Description

### My Test API main wrappers

<a name="doc_generator_test.ClientRequest"></a>

#### doc_generator_test.ClientRequest message description:

Client request with specified action.

| Field                    | Type                                                                              | Label | Description                                                             |
|--------------------------|-----------------------------------------------------------------------------------|-------|-------------------------------------------------------------------------|
| **trx**                  | [string](#string)                                                                 |       | unique transaction id of each message to match it with server response. |
| **token_request**        | [doc_generator_test.TokenRequest](#doc_generator_test.TokenRequest)               |       |                                                                         |
| **registration_request** | [doc_generator_test.RegistrationRequest](#doc_generator_test.RegistrationRequest) |       |                                                                         |

### 

<a name="doc_generator_test.ServerResponse"></a>

#### doc_generator_test.ServerResponse message description:

Server response with specified action.

| Field              | Type                                                                                | Label | Description |
|--------------------|-------------------------------------------------------------------------------------|-------|-------------|
| **trx**            | [string](#string)                                                                   |       |             |
| **token_response** | [doc_generator_test.TokenRequest](#doc_generator_test.TokenRequest)                 |       |             |
| **login_request**  | [doc_generator_test.RegistrationResponse](#doc_generator_test.RegistrationResponse) |       |             |

<a name="doc_generator_test.TokenRequest"></a>

#### doc_generator_test.TokenRequest message description:

Token request for external API. Returns authorization bearer token (JWT token) payload if login is successful.

| Field        | Type              | Label | Description                        |
|--------------|-------------------|-------|------------------------------------|
| **username** | [string](#string) |       | user name (email)                  |
| **password** | [string](#string) |       | password                           |
| **expiry**   | [int64](#int64)   |       | token expiration period in seconds |

#### 'TokenRequest' code example:

```
{
    "trx": "783b9df7-4ab2-481d-8a26-cf30908b673f",
    "tokenRequest": {
        "username": "{{USER}}",
        "password": "{{PASS}}",
        "expiry": 2147483647
    }
}
```

<a name="doc_generator_test.TokenResponse"></a>

#### doc_generator_test.TokenResponse message description:

Token response.

| Field            | Type                                                              | Label | Description                                                                                                      |
|------------------|-------------------------------------------------------------------|-------|------------------------------------------------------------------------------------------------------------------|
| **status**       | [doc_generator_test.LoginStatus](#doc_generator_test.LoginStatus) |       | login status                                                                                                     |
| **token**        | [string](#string)                                                 |       | base64 encoded jwt token to include in authorization bearer header that you need to decode to use in your header |
| **valid_till**   | [int64](#int64)                                                   |       | valid till (unix timestamp in seconds)                                                                           |
| **error_reason** | [string](#string)                                                 |       | error reason if error has occurred                                                                               |
```
