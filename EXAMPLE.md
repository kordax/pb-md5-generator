# Example Output

This file is a generated markdown example for the larger integration fixture: `testdata/integration/full_api.proto`.

It demonstrates the renderer output for annotated protobuf definitions, including generated and explicit code examples, nested messages, maps, repeated fields, enums, oneof payloads, and ignored definitions.

```console
./build/pb-md5-generator -d testdata/integration -f testdata/integration/full_api.proto -o EXAMPLE.md
```



* Table Of Contents
     * [CreateUserRequest](#fixture.CreateUserRequest)
     * [CreateUserResponse](#fixture.CreateUserResponse)
     * [SessionResponse](#fixture.SessionResponse)
     * [SessionAuditRequest](#fixture.SessionAuditRequest)
     * [Profile](#fixture.Profile)
     * [CreatePaymentRequest](#fixture.CreatePaymentRequest)
     * [CreatePaymentResponse](#fixture.CreatePaymentResponse)
     * [RefundPaymentRequest](#fixture.RefundPaymentRequest)
     * [SearchCatalogRequest](#fixture.SearchCatalogRequest)
     * [InventorySnapshot](#fixture.InventorySnapshot)
     * [AuditQueryRequest](#fixture.AuditQueryRequest)
     * [AuditEnvelope](#fixture.AuditEnvelope)
     * [NotificationBatch](#fixture.NotificationBatch)

* Enums
     * [UserStatus](#fixture.UserStatus)
     * [Role](#fixture.Role)
     * [PaymentStatus](#fixture.PaymentStatus)
     * [Currency](#fixture.Currency)
     * [Region](#fixture.Region)
     * [InventoryState](#fixture.InventoryState)
     * [AuditAction](#fixture.AuditAction)
     * [NotificationChannel](#fixture.NotificationChannel)

# Fixture API

## API Description

### Users

<a name="fixture.CreateUserRequest"></a>

#### `fixture.CreateUserRequest` message description:

Creates a user with generated JSON example.

| Field              | Type                          | Label          | Description                       | Min value | Max value | Max length/size |
|--------------------|-------------------------------|----------------|-----------------------------------|-----------|-----------|-----------------|
| **`email`**        | [string](#string)             |                | Email field description.          |           |           |                 |
| **`phone`**        | [string](#string)             |                | Phone field description.          |           |           |                 |
| **`password`**     | [string](#string)             |                | Password field description.       |           |           |                 |
| **`request_uuid`** | [string](#string)             |                | UUID field description.           |           |           |                 |
| **`access_token`** | [string](#string)             |                | JWT field description.            |           |           |                 |
| **`age`**          | [int32](#int32)               |                | Age field description.            | 18        | 100       |                 |
| **`score`**        | [float64](#float64)           |                | Score field description.          | 0.5       | 99.9      |                 |
| **`login_count`**  | [uint64](#uint64)             |                | Login count field description.    | 1         | 42        |                 |
| **`display_name`** | [string](#string)             |                | Display name field description.   |           |           | 12              |
| **`nickname`**     | [string](#string)             |                | Fixed nickname field description. |           |           |                 |
| **`enabled`**      | [bool](#bool)                 |                | Fixed bool field description.     |           |           |                 |
| **`role`**         | [fixture.Role](#fixture.Role) |                | Role enum field description.      |           |           |                 |
| **`tags`**         | [string](#string)             | LABEL_REPEATED | Repeated tags field description.  |           |           |                 |
| **`raw_payload`**  | [[]byte](#[]byte)             |                | Bytes payload field description.  |           |           |                 |

#### `CreateUserRequest` code example:

```
{
	"CreateUserRequest": {
		"access_token": "example-access-token",
		"age": 59,
		"display_name": "purple-reson",
		"email": "dawn-star@email.com",
		"enabled": true,
		"login_count": 11,
		"nickname": "fixed-nickname",
		"password": "C4VYA#220b108",
		"phone": "+991.3027123964",
		"raw_payload": "cool-frost",
		"request_uuid": "cb68c0a9-1348-415a-81f7-5809056cf53e",
		"role": "ROLE_ADMIN",
		"score": 15.923665950010314,
		"tags": "wispy-paper"
	},
	"trx": "3bd733e6-c5c8-4336-a6fe-2e525df3e70d"
}
```

<a name="fixture.CreateUserResponse"></a>

#### `fixture.CreateUserResponse` message description:

Explicit JSON example message.

| Field         | Type                                      | Label          | Description                        |
|---------------|-------------------------------------------|----------------|------------------------------------|
| **`status`**  | [fixture.UserStatus](#fixture.UserStatus) |                | Response status field description. |
| **`user_id`** | [string](#string)                         |                | User id field description.         |
| **`roles`**   | [fixture.Role](#fixture.Role)             | LABEL_REPEATED | Roles field description.           |

#### `CreateUserResponse` code example:

```
{
	"CreateUserResponse": {
		"status": "USER_STATUS_ACTIVE",
		"user_id": "usr_123",
		"roles": [
			"ROLE_ADMIN"
		]
	}
}
```

### Sessions

<a name="fixture.SessionResponse"></a>

#### `fixture.SessionResponse` message description:

XML code example message.

| Field            | Type              | Label | Description                               | Max length/size |
|------------------|-------------------|-------|-------------------------------------------|-----------------|
| **`id`**         | [string](#string) |       | Session identifier description.           | 16              |
| **`expires_at`** | [string](#string) |       | Session expiration timestamp description. |                 |

#### `SessionResponse` code example:

```
<session>
  <id>session-1</id>
</session>
```

<a name="fixture.SessionAuditRequest"></a>

#### `fixture.SessionAuditRequest` message description:

XML autocode marker coverage.

| Field            | Type                | Label | Description |
|------------------|---------------------|-------|-------------|
| **`session_id`** | [string](#string)   |       |             |
| **`active`**     | [bool](#bool)       |       |             |
| **`risk_score`** | [float32](#float32) |       |             |

#### `SessionAuditRequest` code example:

```
{
	"SessionAuditRequest": {
		"active": false,
		"risk_score": 354174.87737456034,
		"session_id": "session-1"
	},
	"trx": "668f0416-0408-47a8-8d9b-ae2f7b120645"
}
```

<a name="fixture.Profile"></a>

#### `fixture.Profile` message description:

Parent message description.

| Field                    | Type                                                        | Label          | Description             | Max length/size |
|--------------------------|-------------------------------------------------------------|----------------|-------------------------|-----------------|
| **`first_name`**         | [string](#string)                                           |                | First name description. | 32              |
| **`last_name`**          | [string](#string)                                           |                | Last name description.  | 64              |
| **`primary_address`**    | [fixture.Profile.Address](#fixture.Profile.Address)         |                |                         |                 |
| **`previous_addresses`** | [fixture.Profile.Address](#fixture.Profile.Address)         | LABEL_REPEATED |                         |                 |
| **`preferences`**        | [fixture.Profile.Preferences](#fixture.Profile.Preferences) |                |                         |                 |

<a name="fixture.Profile.Address"></a>

#### `fixture.Profile.Address` message description:

Nested address description.

| Field         | Type                                                                  | Label | Description               | Max length/size |
|---------------|-----------------------------------------------------------------------|-------|---------------------------|-----------------|
| **`country`** | [string](#string)                                                     |       | Country code description. | 2               |
| **`city`**    | [string](#string)                                                     |       | City description.         | 64              |
| **`line`**    | [string](#string)                                                     |       | Street line description.  | 128             |
| **`geo`**     | [fixture.Profile.Address.GeoPoint](#fixture.Profile.Address.GeoPoint) |       |                           |                 |

<a name="fixture.Profile.Address.GeoPoint"></a>

#### `fixture.Profile.Address.GeoPoint` message description:

Nested geo point description.

| Field     | Type                | Label | Description            | Min value | Max value |
|-----------|---------------------|-------|------------------------|-----------|-----------|
| **`lat`** | [float64](#float64) |       | Latitude description.  | -90       | 90        |
| **`lon`** | [float64](#float64) |       | Longitude description. | -180      | 180       |

<a name="fixture.Profile.Preferences"></a>

#### `fixture.Profile.Preferences` message description:

Nested preferences description.

| Field                   | Type                                                                                    | Label          | Description                      | Max length/size |
|-------------------------|-----------------------------------------------------------------------------------------|----------------|----------------------------------|-----------------|
| **`marketing_enabled`** | [bool](#bool)                                                                           |                | Marketing enabled description.   |                 |
| **`languages`**         | [string](#string)                                                                       | LABEL_REPEATED | Preferred languages description. | 5               |
| **`settings`**          | [fixture.Profile.Preferences.SettingsEntry](#fixture.Profile.Preferences.SettingsEntry) | LABEL_REPEATED | Settings map description.        |                 |

<a name="fixture.Profile.Preferences.SettingsEntry"></a>

#### `fixture.Profile.Preferences.SettingsEntry` message:

| Field       | Type              | Label | Description |
|-------------|-------------------|-------|-------------|
| **`key`**   | [string](#string) |       |             |
| **`value`** | [string](#string) |       |             |

### Billing

<a name="fixture.CreatePaymentRequest"></a>

#### `fixture.CreatePaymentRequest` message description:

Creates a payment using generated JSON example.

| Field                 | Type                                  | Label | Description                     | Min value | Max value | Max length/size |
|-----------------------|---------------------------------------|-------|---------------------------------|-----------|-----------|-----------------|
| **`merchant_id`**     | [string](#string)                     |       | Merchant id description.        |           |           | 12              |
| **`order_id`**        | [string](#string)                     |       | Order id description.           |           |           | 10              |
| **`amount_minor`**    | [int64](#int64)                       |       | Amount minor units description. | 100       | 999999    |                 |
| **`currency`**        | [fixture.Currency](#fixture.Currency) |       | Currency enum description.      |           |           |                 |
| **`capture`**         | [bool](#bool)                         |       | Capture flag description.       |           |           |                 |
| **`idempotency_key`** | [string](#string)                     |       | Idempotency key description.    |           |           |                 |
| **`customer_email`**  | [string](#string)                     |       | Customer email description.     |           |           | 128             |
| **`risk_score`**      | [float32](#float32)                   |       | Risk score description.         | 0         | 1         |                 |
| **`attempt`**         | [uint32](#uint32)                     |       | Attempt count description.      | 1         | 5         |                 |

#### `CreatePaymentRequest` code example:

```
{
	"CreatePaymentRequest": {
		"amount_minor": 10867,
		"attempt": 3,
		"capture": false,
		"currency": "CURRENCY_KZT",
		"customer_email": "summer-fire@email.com",
		"idempotency_key": "example-idempotency-key",
		"merchant_id": "merchant-001",
		"order_id": "order-9001",
		"risk_score": 0.8420787705465779
	},
	"trx": "1250a8f8-2641-4b73-83b5-b57eef30cffc"
}
```

<a name="fixture.CreatePaymentResponse"></a>

#### `fixture.CreatePaymentResponse` message description:

Rich payment response with nested structures and arrays.

| Field              | Type                                                                                        | Label          | Description                     | Min value | Max value | Max length/size |
|--------------------|---------------------------------------------------------------------------------------------|----------------|---------------------------------|-----------|-----------|-----------------|
| **`payment_id`**   | [string](#string)                                                                           |                | Payment id description.         |           |           | 16              |
| **`status`**       | [fixture.PaymentStatus](#fixture.PaymentStatus)                                             |                | Payment status description.     |           |           |                 |
| **`amount_minor`** | [int64](#int64)                                                                             |                | Amount minor units description. | 1         | 999999    |                 |
| **`currency`**     | [fixture.Currency](#fixture.Currency)                                                       |                | Currency description.           |           |           |                 |
| **`created_at`**   | [string](#string)                                                                           |                | Created timestamp description.  |           |           |                 |
| **`customer`**     | [fixture.CreatePaymentResponse.Customer](#fixture.CreatePaymentResponse.Customer)           |                |                                 |           |           |                 |
| **`method`**       | [fixture.CreatePaymentResponse.PaymentMethod](#fixture.CreatePaymentResponse.PaymentMethod) |                |                                 |           |           |                 |
| **`fees`**         | [fixture.CreatePaymentResponse.Fee](#fixture.CreatePaymentResponse.Fee)                     | LABEL_REPEATED |                                 |           |           |                 |
| **`metadata`**     | [fixture.CreatePaymentResponse.MetadataEntry](#fixture.CreatePaymentResponse.MetadataEntry) | LABEL_REPEATED |                                 |           |           |                 |

#### `CreatePaymentResponse` code example:

```
{
	"CreatePaymentResponse": {
		"payment_id": "pay_8f3c0d",
		"status": "PAYMENT_STATUS_CAPTURED",
		"amount_minor": 125000,
		"currency": "CURRENCY_USD",
		"created_at": "2026-07-09T08:00:00Z",
		"customer": {
			"id": "cus_001",
			"email": "alice@example.com",
			"phones": [
				"+7.7000000000",
				"+1.5550000000"
			]
		},
		"method": {
			"kind": "bank_card",
			"card": {
				"brand": "visa",
				"last4": "4242",
				"exp_month": 12,
				"exp_year": 2030
			}
		},
		"fees": [
			{
				"type": "processing",
				"amount_minor": 320
			},
			{
				"type": "network",
				"amount_minor": 25
			}
		],
		"metadata": {
			"order": "order-9001",
			"tenant": "fixture",
			"region": "eu-west"
		}
	}
}
```

<a name="fixture.CreatePaymentResponse.Customer"></a>

#### `fixture.CreatePaymentResponse.Customer` message:

| Field        | Type              | Label          | Description                      | Max length/size |
|--------------|-------------------|----------------|----------------------------------|-----------------|
| **`id`**     | [string](#string) |                | Customer id description.         | 16              |
| **`email`**  | [string](#string) |                | Customer email description.      | 128             |
| **`phones`** | [string](#string) | LABEL_REPEATED | Customer phone list description. |                 |

<a name="fixture.CreatePaymentResponse.PaymentMethod"></a>

#### `fixture.CreatePaymentResponse.PaymentMethod` message:

| Field      | Type                                                                                                  | Label | Description                      |
|------------|-------------------------------------------------------------------------------------------------------|-------|----------------------------------|
| **`kind`** | [string](#string)                                                                                     |       | Payment method kind description. |
| **`card`** | [fixture.CreatePaymentResponse.PaymentMethod.Card](#fixture.CreatePaymentResponse.PaymentMethod.Card) |       |                                  |

<a name="fixture.CreatePaymentResponse.PaymentMethod.Card"></a>

#### `fixture.CreatePaymentResponse.PaymentMethod.Card` message:

| Field           | Type              | Label | Description                        | Min value | Max value | Max length/size |
|-----------------|-------------------|-------|------------------------------------|-----------|-----------|-----------------|
| **`brand`**     | [string](#string) |       | Card brand description.            |           |           |                 |
| **`last4`**     | [string](#string) |       | Card last four digits description. |           |           | 4               |
| **`exp_month`** | [int32](#int32)   |       | Expiration month description.      | 1         | 12        |                 |
| **`exp_year`**  | [int32](#int32)   |       | Expiration year description.       | 2026      | 2040      |                 |

<a name="fixture.CreatePaymentResponse.Fee"></a>

#### `fixture.CreatePaymentResponse.Fee` message:

| Field              | Type              | Label | Description                         | Min value | Max value |
|--------------------|-------------------|-------|-------------------------------------|-----------|-----------|
| **`type`**         | [string](#string) |       | Fee type description.               |           |           |
| **`amount_minor`** | [int64](#int64)   |       | Fee amount minor units description. | 0         | 10000     |

<a name="fixture.CreatePaymentResponse.MetadataEntry"></a>

#### `fixture.CreatePaymentResponse.MetadataEntry` message:

| Field       | Type              | Label | Description |
|-------------|-------------------|-------|-------------|
| **`key`**   | [string](#string) |       |             |
| **`value`** | [string](#string) |       |             |

<a name="fixture.RefundPaymentRequest"></a>

#### `fixture.RefundPaymentRequest` message description:

XML refund example with nested elements.

| Field              | Type                                                                    | Label          | Description |
|--------------------|-------------------------------------------------------------------------|----------------|-------------|
| **`payment_id`**   | [string](#string)                                                       |                |             |
| **`amount_minor`** | [int64](#int64)                                                         |                |             |
| **`reason`**       | [string](#string)                                                       |                |             |
| **`items`**        | [fixture.RefundPaymentRequest.Item](#fixture.RefundPaymentRequest.Item) | LABEL_REPEATED |             |

#### `RefundPaymentRequest` code example:

```
<refund>
  <payment_id>pay_8f3c0d</payment_id>
  <amount_minor>25000</amount_minor>
  <reason>customer_request</reason>
  <items>
    <item>
      <sku>sku-keyboard</sku>
      <quantity>1</quantity>
    </item>
    <item>
      <sku>sku-mouse</sku>
      <quantity>2</quantity>
    </item>
  </items>
</refund>
```

<a name="fixture.RefundPaymentRequest.Item"></a>

#### `fixture.RefundPaymentRequest.Item` message:

| Field          | Type              | Label | Description |
|----------------|-------------------|-------|-------------|
| **`sku`**      | [string](#string) |       |             |
| **`quantity`** | [int32](#int32)   |       |             |

### Inventory

<a name="fixture.SearchCatalogRequest"></a>

#### `fixture.SearchCatalogRequest` message description:

Catalog search request with generated example.

| Field                  | Type                              | Label | Description                   | Min value | Max value | Max length/size |
|------------------------|-----------------------------------|-------|-------------------------------|-----------|-----------|-----------------|
| **`query`**            | [string](#string)                 |       | Search query description.     |           |           | 64              |
| **`page`**             | [int32](#int32)                   |       | Page number description.      | 1         | 20        |                 |
| **`page_size`**        | [int32](#int32)                   |       | Page size description.        | 10        | 100       |                 |
| **`region`**           | [fixture.Region](#fixture.Region) |       | Region description.           |           |           |                 |
| **`include_archived`** | [bool](#bool)                     |       | Include archived description. |           |           |                 |

#### `SearchCatalogRequest` code example:

```
{
	"SearchCatalogRequest": {
		"include_archived": false,
		"page": 15,
		"page_size": 43,
		"query": "mechanical-keyboard",
		"region": "REGION_US_EAST"
	},
	"trx": "13cf38e6-eec0-48da-b487-ff5ea34375ac"
}
```

<a name="fixture.InventorySnapshot"></a>

#### `fixture.InventorySnapshot` message description:

Inventory snapshot with maps, repeated rows and deeply nested dimensions.

| Field              | Type                                                                                | Label          | Description               | Max length/size |
|--------------------|-------------------------------------------------------------------------------------|----------------|---------------------------|-----------------|
| **`warehouse_id`** | [string](#string)                                                                   |                | Warehouse id description. | 16              |
| **`region`**       | [fixture.Region](#fixture.Region)                                                   |                | Region description.       |                 |
| **`items`**        | [fixture.InventorySnapshot.Item](#fixture.InventorySnapshot.Item)                   | LABEL_REPEATED |                           |                 |
| **`counters`**     | [fixture.InventorySnapshot.CountersEntry](#fixture.InventorySnapshot.CountersEntry) | LABEL_REPEATED |                           |                 |

#### `InventorySnapshot` code example:

```
{
	"InventorySnapshot": {
		"warehouse_id": "wh-eu-1",
		"region": "REGION_EU_WEST",
		"items": [
			{
				"sku": "sku-keyboard",
				"state": "INVENTORY_STATE_AVAILABLE",
				"quantity": 128,
				"dimensions": {
					"width": 45.5,
					"height": 4.2,
					"depth": 15.0
				},
				"labels": {
					"color": "black",
					"layout": "ansi"
				}
			},
			{
				"sku": "sku-mouse",
				"state": "INVENTORY_STATE_RESERVED",
				"quantity": 64,
				"dimensions": {
					"width": 7.2,
					"height": 4.0,
					"depth": 12.1
				},
				"labels": {
					"wireless": "true",
					"dpi": "16000"
				}
			}
		],
		"counters": {
			"available": 128,
			"reserved": 64,
			"damaged": 3
		}
	}
}
```

<a name="fixture.InventorySnapshot.Item"></a>

#### `fixture.InventorySnapshot.Item` message:

| Field            | Type                                                                                      | Label          | Description                  | Min value | Max value | Max length/size |
|------------------|-------------------------------------------------------------------------------------------|----------------|------------------------------|-----------|-----------|-----------------|
| **`sku`**        | [string](#string)                                                                         |                | SKU description.             |           |           | 32              |
| **`state`**      | [fixture.InventoryState](#fixture.InventoryState)                                         |                | Inventory state description. |           |           |                 |
| **`quantity`**   | [int64](#int64)                                                                           |                | Quantity description.        | 0         | 100000    |                 |
| **`dimensions`** | [fixture.InventorySnapshot.Item.Dimensions](#fixture.InventorySnapshot.Item.Dimensions)   |                |                              |           |           |                 |
| **`labels`**     | [fixture.InventorySnapshot.Item.LabelsEntry](#fixture.InventorySnapshot.Item.LabelsEntry) | LABEL_REPEATED |                              |           |           |                 |

<a name="fixture.InventorySnapshot.Item.Dimensions"></a>

#### `fixture.InventorySnapshot.Item.Dimensions` message:

| Field        | Type                | Label | Description         | Min value | Max value |
|--------------|---------------------|-------|---------------------|-----------|-----------|
| **`width`**  | [float64](#float64) |       | Width description.  | 0.1       | 999.9     |
| **`height`** | [float64](#float64) |       | Height description. | 0.1       | 999.9     |
| **`depth`**  | [float64](#float64) |       | Depth description.  | 0.1       | 999.9     |

<a name="fixture.InventorySnapshot.Item.LabelsEntry"></a>

#### `fixture.InventorySnapshot.Item.LabelsEntry` message:

| Field       | Type              | Label | Description |
|-------------|-------------------|-------|-------------|
| **`key`**   | [string](#string) |       |             |
| **`value`** | [string](#string) |       |             |

<a name="fixture.InventorySnapshot.CountersEntry"></a>

#### `fixture.InventorySnapshot.CountersEntry` message:

| Field       | Type              | Label | Description |
|-------------|-------------------|-------|-------------|
| **`key`**   | [string](#string) |       |             |
| **`value`** | [int64](#int64)   |       |             |

### Audit

<a name="fixture.AuditQueryRequest"></a>

#### `fixture.AuditQueryRequest` message description:

Audit query request with several generated constrained values.

| Field             | Type                                        | Label | Description                 | Min value | Max value | Max length/size |
|-------------------|---------------------------------------------|-------|-----------------------------|-----------|-----------|-----------------|
| **`tenant_id`**   | [string](#string)                           |       | Tenant description.         |           |           | 32              |
| **`actor_email`** | [string](#string)                           |       | Actor description.          |           |           | 128             |
| **`action`**      | [fixture.AuditAction](#fixture.AuditAction) |       | Action description.         |           |           |                 |
| **`from`**        | [string](#string)                           |       | From timestamp description. |           |           |                 |
| **`to`**          | [string](#string)                           |       | To timestamp description.   |           |           |                 |
| **`limit`**       | [int32](#int32)                             |       | Limit description.          | 1         | 500       |                 |

#### `AuditQueryRequest` code example:

```
{
	"AuditQueryRequest": {
		"action": "AUDIT_ACTION_CREATED",
		"actor_email": "broken-forest@email.com",
		"from": "2026-07-09T00:00:00Z",
		"limit": 168,
		"tenant_id": "tenant-fixture",
		"to": "2026-07-09T23:59:59Z"
	},
	"trx": "4c9a6909-656e-429f-8293-fc3e4a662cb9"
}
```

<a name="fixture.AuditEnvelope"></a>

#### `fixture.AuditEnvelope` message description:

Audit envelope with nested actor, target, diff and context structures.

| Field          | Type                                                            | Label          | Description               | Max length/size |
|----------------|-----------------------------------------------------------------|----------------|---------------------------|-----------------|
| **`event_id`** | [string](#string)                                               |                | Event id description.     | 32              |
| **`action`**   | [fixture.AuditAction](#fixture.AuditAction)                     |                | Audit action description. |                 |
| **`actor`**    | [fixture.AuditEnvelope.Actor](#fixture.AuditEnvelope.Actor)     |                |                           |                 |
| **`target`**   | [fixture.AuditEnvelope.Target](#fixture.AuditEnvelope.Target)   |                |                           |                 |
| **`diffs`**    | [fixture.AuditEnvelope.Diff](#fixture.AuditEnvelope.Diff)       | LABEL_REPEATED |                           |                 |
| **`context`**  | [fixture.AuditEnvelope.Context](#fixture.AuditEnvelope.Context) |                |                           |                 |

#### `AuditEnvelope` code example:

```
{
	"AuditEnvelope": {
		"event_id": "evt_001",
		"action": "AUDIT_ACTION_UPDATED",
		"actor": {
			"id": "usr_123",
			"email": "admin@example.com",
			"ip": "203.0.113.10",
			"user_agent": "fixture-cli/1.0"
		},
		"target": {
			"type": "payment",
			"id": "pay_8f3c0d",
			"display_name": "Payment pay_8f3c0d"
		},
		"diffs": [
			{
				"path": "status",
				"before": "PAYMENT_STATUS_PENDING",
				"after": "PAYMENT_STATUS_CAPTURED"
			},
			{
				"path": "metadata.region",
				"before": "us-east",
				"after": "eu-west"
			}
		],
		"context": {
			"request_id": "req_001",
			"trace_id": "trace_abcdef",
			"tags": [
				"integration",
				"audit",
				"fixture"
			]
		}
	}
}
```

<a name="fixture.AuditEnvelope.Actor"></a>

#### `fixture.AuditEnvelope.Actor` message:

| Field            | Type              | Label | Description                   | Max length/size |
|------------------|-------------------|-------|-------------------------------|-----------------|
| **`id`**         | [string](#string) |       | Actor id description.         | 16              |
| **`email`**      | [string](#string) |       | Actor email description.      | 128             |
| **`ip`**         | [string](#string) |       | Actor IP address description. |                 |
| **`user_agent`** | [string](#string) |       | Actor user agent description. | 128             |

<a name="fixture.AuditEnvelope.Target"></a>

#### `fixture.AuditEnvelope.Target` message:

| Field              | Type              | Label | Description                      | Max length/size |
|--------------------|-------------------|-------|----------------------------------|-----------------|
| **`type`**         | [string](#string) |       | Target type description.         |                 |
| **`id`**           | [string](#string) |       | Target id description.           | 32              |
| **`display_name`** | [string](#string) |       | Target display name description. | 128             |

<a name="fixture.AuditEnvelope.Diff"></a>

#### `fixture.AuditEnvelope.Diff` message:

| Field        | Type              | Label | Description                 | Max length/size |
|--------------|-------------------|-------|-----------------------------|-----------------|
| **`path`**   | [string](#string) |       | Diff path description.      | 64              |
| **`before`** | [string](#string) |       | Previous value description. | 128             |
| **`after`**  | [string](#string) |       | New value description.      | 128             |

<a name="fixture.AuditEnvelope.Context"></a>

#### `fixture.AuditEnvelope.Context` message:

| Field            | Type              | Label          | Description               | Max length/size |
|------------------|-------------------|----------------|---------------------------|-----------------|
| **`request_id`** | [string](#string) |                | Request id description.   | 32              |
| **`trace_id`**   | [string](#string) |                | Trace id description.     | 64              |
| **`tags`**       | [string](#string) | LABEL_REPEATED | Context tags description. | 32              |

### Notifications

<a name="fixture.NotificationBatch"></a>

#### `fixture.NotificationBatch` message description:

Notification batch with oneof payload alternatives and nested recipient details.

| Field            | Type                                                                              | Label          | Description                       | Max length/size |
|------------------|-----------------------------------------------------------------------------------|----------------|-----------------------------------|-----------------|
| **`batch_id`**   | [string](#string)                                                                 |                | Batch id description.             | 32              |
| **`channel`**    | [fixture.NotificationChannel](#fixture.NotificationChannel)                       |                | Notification channel description. |                 |
| **`recipients`** | [fixture.NotificationBatch.Recipient](#fixture.NotificationBatch.Recipient)       | LABEL_REPEATED |                                   |                 |
| **`email`**      | [fixture.NotificationBatch.EmailPayload](#fixture.NotificationBatch.EmailPayload) |                |                                   |                 |
| **`sms`**        | [fixture.NotificationBatch.SmsPayload](#fixture.NotificationBatch.SmsPayload)     |                |                                   |                 |
| **`push`**       | [fixture.NotificationBatch.PushPayload](#fixture.NotificationBatch.PushPayload)   |                |                                   |                 |

#### `NotificationBatch` code example:

```
{
	"NotificationBatch": {
		"batch_id": "batch-001",
		"channel": "NOTIFICATION_CHANNEL_EMAIL",
		"recipients": [
			{
				"user_id": "usr_123",
				"email": "alice@example.com",
				"variables": {
					"first_name": "Alice",
					"tier": "gold"
				}
			},
			{
				"user_id": "usr_456",
				"email": "bob@example.com",
				"variables": {
					"first_name": "Bob",
					"tier": "silver"
				}
			}
		],
		"email": {
			"subject": "Fixture notification",
			"template_id": "tpl-welcome",
			"reply_to": "support@example.com"
		}
	}
}
```

<a name="fixture.NotificationBatch.Recipient"></a>

#### `fixture.NotificationBatch.Recipient` message:

| Field           | Type                                                                                                      | Label          | Description                      | Max length/size |
|-----------------|-----------------------------------------------------------------------------------------------------------|----------------|----------------------------------|-----------------|
| **`user_id`**   | [string](#string)                                                                                         |                | Recipient user id description.   | 16              |
| **`email`**     | [string](#string)                                                                                         |                | Recipient email description.     | 128             |
| **`phone`**     | [string](#string)                                                                                         |                | Recipient phone description.     |                 |
| **`variables`** | [fixture.NotificationBatch.Recipient.VariablesEntry](#fixture.NotificationBatch.Recipient.VariablesEntry) | LABEL_REPEATED | Recipient variables description. |                 |

<a name="fixture.NotificationBatch.Recipient.VariablesEntry"></a>

#### `fixture.NotificationBatch.Recipient.VariablesEntry` message:

| Field       | Type              | Label | Description |
|-------------|-------------------|-------|-------------|
| **`key`**   | [string](#string) |       |             |
| **`value`** | [string](#string) |       |             |

<a name="fixture.NotificationBatch.EmailPayload"></a>

#### `fixture.NotificationBatch.EmailPayload` message:

| Field             | Type              | Label | Description                    | Max length/size |
|-------------------|-------------------|-------|--------------------------------|-----------------|
| **`subject`**     | [string](#string) |       | Email subject description.     | 128             |
| **`template_id`** | [string](#string) |       | Email template id description. | 64              |
| **`reply_to`**    | [string](#string) |       | Reply-to email description.    | 128             |

<a name="fixture.NotificationBatch.SmsPayload"></a>

#### `fixture.NotificationBatch.SmsPayload` message:

| Field        | Type              | Label | Description             | Max length/size |
|--------------|-------------------|-------|-------------------------|-----------------|
| **`sender`** | [string](#string) |       | SMS sender description. | 32              |
| **`body`**   | [string](#string) |       | SMS body description.   | 160             |

<a name="fixture.NotificationBatch.PushPayload"></a>

#### `fixture.NotificationBatch.PushPayload` message:

| Field       | Type                                                                                                | Label          | Description             | Max length/size |
|-------------|-----------------------------------------------------------------------------------------------------|----------------|-------------------------|-----------------|
| **`title`** | [string](#string)                                                                                   |                | Push title description. | 64              |
| **`body`**  | [string](#string)                                                                                   |                | Push body description.  | 256             |
| **`data`**  | [fixture.NotificationBatch.PushPayload.DataEntry](#fixture.NotificationBatch.PushPayload.DataEntry) | LABEL_REPEATED | Push data description.  |                 |

<a name="fixture.NotificationBatch.PushPayload.DataEntry"></a>

#### `fixture.NotificationBatch.PushPayload.DataEntry` message:

| Field       | Type              | Label | Description |
|-------------|-------------------|-------|-------------|
| **`key`**   | [string](#string) |       |             |
| **`value`** | [string](#string) |       |             |

## Enums

<a name="fixture.UserStatus"></a>

#### `fixture.UserStatus` enum:

| Value                       | Description                   |
|-----------------------------|-------------------------------|
| **`USER_STATUS_UNKNOWN`**   | Unknown status description.   |
| **`USER_STATUS_ACTIVE`**    | Active status description.    |
| **`USER_STATUS_SUSPENDED`** | Suspended status description. |

<a name="fixture.Role"></a>

#### `fixture.Role` enum description:

Role enum description.

| Value                  | Description                              |
|------------------------|------------------------------------------|
| **`ROLE_UNSPECIFIED`** | Role is not specified.                   |
| **`ROLE_ADMIN`**       | Administrator role with full access.     |
| **`ROLE_MEMBER`**      | Regular member role with limited access. |

<a name="fixture.PaymentStatus"></a>

#### `fixture.PaymentStatus` enum:

| Value                         | Description                                     |
|-------------------------------|-------------------------------------------------|
| **`PAYMENT_STATUS_UNKNOWN`**  | Payment status is unknown.                      |
| **`PAYMENT_STATUS_PENDING`**  | Payment is waiting for capture or confirmation. |
| **`PAYMENT_STATUS_CAPTURED`** | Payment has been captured successfully.         |
| **`PAYMENT_STATUS_DECLINED`** | Payment was declined by provider or issuer.     |
| **`PAYMENT_STATUS_REFUNDED`** | Payment has been refunded.                      |

<a name="fixture.Currency"></a>

#### `fixture.Currency` enum description:

Currency enum description.

| Value                  | Description           |
|------------------------|-----------------------|
| **`CURRENCY_UNKNOWN`** | Currency is unknown.  |
| **`CURRENCY_USD`**     | United States dollar. |
| **`CURRENCY_EUR`**     | Euro.                 |
| **`CURRENCY_KZT`**     | Kazakhstani tenge.    |

<a name="fixture.Region"></a>

#### `fixture.Region` enum:

| Value                 | Description        |
|-----------------------|--------------------|
| **`REGION_UNKNOWN`**  | Region is unknown. |
| **`REGION_US_EAST`**  | US East region.    |
| **`REGION_EU_WEST`**  | EU West region.    |
| **`REGION_AP_SOUTH`** | AP South region.   |

<a name="fixture.InventoryState"></a>

#### `fixture.InventoryState` enum description:

Inventory state enum description.

| Value                           | Description                  |
|---------------------------------|------------------------------|
| **`INVENTORY_STATE_UNKNOWN`**   | Inventory state is unknown.  |
| **`INVENTORY_STATE_AVAILABLE`** | Inventory item is available. |
| **`INVENTORY_STATE_RESERVED`**  | Inventory item is reserved.  |
| **`INVENTORY_STATE_DAMAGED`**   | Inventory item is damaged.   |

<a name="fixture.AuditAction"></a>

#### `fixture.AuditAction` enum:

| Value                       | Description              |
|-----------------------------|--------------------------|
| **`AUDIT_ACTION_UNKNOWN`**  | Audit action is unknown. |
| **`AUDIT_ACTION_CREATED`**  | Resource was created.    |
| **`AUDIT_ACTION_UPDATED`**  | Resource was updated.    |
| **`AUDIT_ACTION_DELETED`**  | Resource was deleted.    |
| **`AUDIT_ACTION_EXPORTED`** | Resource was exported.   |

<a name="fixture.NotificationChannel"></a>

#### `fixture.NotificationChannel` enum:

| Value                              | Description                      |
|------------------------------------|----------------------------------|
| **`NOTIFICATION_CHANNEL_UNKNOWN`** | Notification channel is unknown. |
| **`NOTIFICATION_CHANNEL_EMAIL`**   | Email notification channel.      |
| **`NOTIFICATION_CHANNEL_SMS`**     | SMS notification channel.        |
| **`NOTIFICATION_CHANNEL_PUSH`**    | Push notification channel.       |
