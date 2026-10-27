/* Progressive API client. Imported contracts and credentials never execute code. */
(function (root, factory) {
  "use strict";
  const client = factory(root);
  if (typeof module === "object" && module.exports) module.exports = client;
  else if (root.document) client.attach(root.document);
})(typeof globalThis === "object" ? globalThis : this, function (root) {
  "use strict";
  const LIMITS = Object.freeze({ input: 16384, url: 16384, body: 1048576, response: 1048576, contract: 16777216, chunks: 4096, parameters: 128, timeout: 30000 });
  const forbiddenHeaders = new Set("accept-charset accept-encoding access-control-request-headers access-control-request-method access-control-request-private-network connection content-length cookie cookie2 date dnt expect host keep-alive origin referer set-cookie te trailer transfer-encoding upgrade via user-agent".split(" "));
  const sensitive = /authorization|cookie|token|secret|password|credential|api[-_]?key/i;
  const own = (object, name) => object != null && Object.prototype.hasOwnProperty.call(object, name);
  const encoder = new root.TextEncoder();
  class ClientError extends Error {}
  function checkText(value, label, limit = LIMITS.input) {
    if (typeof value !== "string" || encoder.encode(value).byteLength > limit || value.includes("\0")) throw new ClientError(label + " is missing, invalid or too large");
    return value;
  }
  function scalar(value) {
    if (typeof value === "string") return checkText(value, "Parameter value");
    if (typeof value === "bigint" || typeof value === "boolean") return checkText(String(value), "Parameter value");
    if (typeof value === "number" && Number.isFinite(value)) {
      if (Number.isInteger(value) && !Number.isSafeInteger(value)) throw new ClientError("Use a quoted string for integers outside JavaScript's exact range");
      return String(value);
    }
    throw new ClientError("Only scalar values and flat arrays or objects can be serialized");
  }
  function flat(value) {
    if (Array.isArray(value)) {
      if (value.length > LIMITS.parameters) throw new ClientError("Too many parameter values");
      return { kind: "array", values: value.map(scalar) };
    }
    if (value && typeof value === "object") {
      const entries = Object.entries(value);
      if (entries.length > LIMITS.parameters) throw new ClientError("Too many object properties");
      return { kind: "object", values: entries.map(([key, entry]) => [checkText(key, "Object property"), scalar(entry)]) };
    }
    return { kind: "scalar", values: scalar(value) };
  }
  function encoded(value, reserved = false) {
    const text = scalar(value);
    if (!reserved) return encodeURIComponent(text).replace(/[!'()*]/g, char => "%" + char.charCodeAt(0).toString(16).toUpperCase());
    if (/[#\[\]&]/.test(text)) throw new ClientError("Percent-encode query delimiters (#, [, ], &) before using allowReserved");
    let result = "";
    for (let index = 0; index < text.length;) {
      const triple = text.slice(index, index + 3);
      if (/^%[0-9a-fA-F]{2}$/.test(triple)) { result += triple; index += 3; continue; }
      const character = String.fromCodePoint(text.codePointAt(index));
      result += ":/?@!$'()*+,;=".includes(character) ? character : encodeURIComponent(character);
      index += character.length;
    }
    return result;
  }
  function headerName(name) {
    checkText(name, "Header name", 256);
    if (!/^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/.test(name)) throw new ClientError("Invalid header name");
    const lower = name.toLowerCase();
    if (forbiddenHeaders.has(lower) || /^(sec-|proxy-)/.test(lower) || /^(x-http-method|x-http-method-override|x-method-override)$/.test(lower)) throw new ClientError("This header is controlled by the browser: " + name);
    return lower;
  }
  function headerValue(value) {
    value = checkText(value, "Header value");
    if (/[\r\n]/.test(value) || /[^\x09\x20-\x7e\x80-\xff]/.test(value)) throw new ClientError("Header values must be single-line HTTP byte strings");
    return value;
  }
  function operationLimits(api, operation) {
    const bounded = (values, label, maximum) => {
      if (values != null && (!Array.isArray(values) || values.length > maximum)) throw new ClientError(label + " exceeds the browser client's limit");
    };
    bounded(operation.parameters, "Operation parameters", LIMITS.parameters);
    const servers = operation.servers || api.servers || [];
    bounded(servers, "Server choices", 32);
    for (const server of servers) bounded(server.variables, "Server variables", 32);
    const requirements = operation.security == null ? api.security || [] : operation.security;
    bounded(requirements, "Authentication alternatives", 32);
    for (const requirement of requirements) if (!requirement || typeof requirement !== "object" || Array.isArray(requirement) || Object.keys(requirement).length > 16) throw new ClientError("Authentication requirements exceed the browser client's limit");
    if (operation.requestBody) bounded(operation.requestBody.content, "Request media choices", 64);
    bounded(operation.responses, "Response definitions", 256);
    let responseMedia = 0;
    for (const response of operation.responses || []) { bounded(response.content, "Response media choices", 64); responseMedia += (response.content || []).length; }
    if (responseMedia > 128) throw new ClientError("Response media choices exceed the browser client's limit");
  }
  function ignoredHeader(parameter) { return parameter.in === "header" && /^(accept|content-type|authorization)$/i.test(parameter.name); }
  function mediaName(value) { return String(value || "").split(";", 1)[0].trim().toLowerCase(); }
  function isJSON(value) { return /^(application\/json|[^/]+\/[^/]+\+json)$/.test(mediaName(value)); }
  function rawMedia(value, media) {
    value = checkText(value, "Serialized content", LIMITS.body);
    const name = mediaName(media);
    if (isJSON(name)) {
      try { JSON.parse(value); } catch (_) { throw new ClientError("Enter valid JSON; it is sent exactly as entered"); }
    } else if (!name.startsWith("text/") && name !== "application/xml" && name !== "application/x-www-form-urlencoded") {
      throw new ClientError("This media type needs an external client: " + name);
    }
    return value;
  }
  function serializeParameter(parameter, value) {
    const location = parameter.in;
    const name = checkText(parameter.name || "", "Parameter name", 256);
    if (location === "cookie") throw new ClientError("Cookie parameters are browser-managed and cannot be set by this client");
    if (location === "querystring") throw new ClientError("Whole-querystring parameters need an external client");
    if (!["path", "query", "header"].includes(location)) throw new ClientError("Unsupported parameter location");
    if (parameter.content && parameter.content.length) {
      if (parameter.content.length !== 1) throw new ClientError("Content parameters must declare exactly one media type");
      const text = rawMedia(value, parameter.content[0].name);
      return location === "query" ? { query: [[name, encoded(text)]] } : location === "header" ? { header: headerValue(text) } : { path: encoded(text) };
    }
    const style = parameter.style || (location === "query" ? "form" : "simple");
    const explode = parameter.explode == null ? style === "form" : parameter.explode;
    const data = flat(value);
    const encode = location === "header" ? scalar : entry => encoded(entry, location === "query" && parameter.allowReserved === true);
    const items = data.kind === "array" ? data.values.map(encode) : data.kind === "object" ? data.values.map(([key, entry]) => [encode(key), encode(entry)]) : encode(data.values);
    const joined = separator => data.kind === "array" ? items.join(separator) : data.kind === "object" ? items.flat().join(separator) : items;
    if (location === "query") {
      if (style === "form") {
        if (data.kind === "array" && explode) return { query: items.map(entry => [name, entry]) };
        if (data.kind === "object" && explode) return { query: data.values.map(([key], index) => [key, items[index][1]]) };
        return { query: [[name, joined(",")]] };
      }
      if (style === "deepObject" && data.kind === "object") return { query: data.values.map(([key], index) => [name + "[" + key + "]", items[index][1]]) };
      if ((style === "spaceDelimited" || style === "pipeDelimited") && !explode && data.kind !== "scalar") return { query: [[name, joined(style === "spaceDelimited" ? "%20" : "%7C")]] };
      throw new ClientError("Unsupported query style/type/explode combination");
    }
    if (style === "simple") {
      const text = data.kind === "object" && explode ? items.map(([key, entry]) => key + "=" + entry).join(",") : joined(",");
      return location === "header" ? { header: headerValue(text) } : { path: text };
    }
    if (location !== "path") throw new ClientError("Header parameters only support simple style");
    if (style === "label") return { path: "." + (data.kind === "object" && explode ? items.map(([key, entry]) => key + "=" + entry).join(".") : joined(explode ? "." : ",")) };
    if (style === "matrix") {
      if (data.kind === "object" && explode) return { path: items.map(([key, entry]) => ";" + key + "=" + entry).join("") };
      if (data.kind === "array" && explode) return { path: items.map(entry => ";" + encoded(name) + "=" + entry).join("") };
      return { path: ";" + encoded(name) + "=" + joined(",") };
    }
    throw new ClientError("Unsupported path parameter style");
  }
  function parseParameterInput(text, parameter) {
    checkText(text, "Parameter input");
    if (parameter.content && parameter.content.length) return text;
    if (/^\s*[\[{\"]/.test(text)) {
      // Preserve numeric lexemes before JSON.parse can round an integer or
      // decimal. Style serialization emits scalar text, not JavaScript numbers.
      const pieces = [];
      for (let index = 0; index < text.length;) {
        if (text[index] === '"') {
          const start = index++;
          let escaped = false;
          while (index < text.length) {
            const character = text[index++];
            if (!escaped && character === '"') break;
            if (!escaped && character === "\\") escaped = true; else escaped = false;
          }
          pieces.push(text.slice(start, index));
        } else if (/[-0-9]/.test(text[index])) {
          const match = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(text.slice(index));
          if (match) { pieces.push('"' + match[0] + '"'); index += match[0].length; }
          else pieces.push(text[index++]);
        } else pieces.push(text[index++]);
      }
      let value;
      try { value = JSON.parse(pieces.join("")); } catch (_) { throw new ClientError("Arrays/objects must be JSON; quote a literal string starting with [, { or a quote"); }
      flat(value); // Nested collections have no portable style representation.
      return value;
    }
    return text;
  }
  function sensitiveParameter(api, parameter) {
    return sensitive.test(parameter.name) || (api.securitySchemes || []).some(scheme => scheme.type === "apiKey" && scheme.in === parameter.in && (parameter.in === "header" ? String(scheme.name).toLowerCase() === parameter.name.toLowerCase() : scheme.name === parameter.name));
  }
  function schemaSample(schemaIndex, id) {
    const seen = new Set();
    for (let depth = 0; id && depth < 16 && !seen.has(id); depth++) {
      seen.add(id);
      const schema = schemaIndex.get(id);
      if (!schema) return undefined;
      if (typeof schema.exampleJSON === "string") return schema.exampleJSON;
      if (typeof schema.defaultJSON === "string") return schema.defaultJSON;
      const ref = schema.definition && schema.definition.$ref;
      if (typeof ref !== "string" || /\$id|\$dynamicRef/.test(Object.keys(schema.definition).join(" "))) return undefined;
      try {
        const file = schema.source && schema.source.file || id.split("#", 1)[0];
        const base = new root.URL(file, "https://docs.invalid/");
        const target = new root.URL(ref, base);
        if (target.origin !== base.origin || target.search) return undefined;
        id = decodeURIComponent(target.pathname.slice(1)) + decodeURIComponent(target.hash || "#");
      } catch (_) { return undefined; }
    }
    return undefined;
  }
  function exampleSample(item, media, schemaIndex) {
    const example = (item.examples || []).find(entry => typeof entry.serializedValue === "string" || typeof entry.valueJSON === "string");
    if (example && typeof example.serializedValue === "string") return { value: example.serializedValue, serialized: true };
    let value = typeof item.exampleJSON === "string" ? item.exampleJSON : example && example.valueJSON;
    if (value === undefined) value = schemaSample(schemaIndex, item.schema);
    if (value === undefined) return undefined;
    // For non-JSON media a JSON string example is its string contents. Numeric
    // companions remain text; never round them through Number or stringify.
    if (media && !isJSON(media) && /^\s*"/.test(value)) {
      try { const decoded = JSON.parse(value); if (typeof decoded === "string") value = decoded; } catch (_) { return undefined; }
    }
    return { value, serialized: false };
  }
  function parameterSample(api, parameter, schemaIndex) {
    if (sensitiveParameter(api, parameter)) return undefined;
    const media = parameter.content && parameter.content[0];
    const ownSample = exampleSample(parameter, media && media.name, schemaIndex);
    if (ownSample) return ownSample;
    const sample = media && exampleSample(media, media.name, schemaIndex);
    // A Media Type example is serialized media content, before the enclosing
    // parameter's URI encoding. A Parameter example describes the whole value.
    return sample ? { value: sample.value, serialized: false } : undefined;
  }
  function serializedParameter(parameter, value) {
    value = checkText(value, "Serialized parameter");
    if (parameter.in === "header") return { header: headerValue(value) };
    if (/%(?![0-9a-fA-F]{2})/.test(value) || /[\x00-\x20\x7f#\\]/.test(value)) throw new ClientError("Serialized URI values must use valid percent-encoding");
    if (parameter.in === "path") {
      if (/[/?]/.test(value)) throw new ClientError("A serialized path parameter cannot contain / or ?");
      return { path: value };
    }
    if (parameter.in === "query") {
      if (!value) return { query: [] };
      return { query: value.split("&").map(pair => {
        const index = pair.indexOf("=");
        if (index < 0) throw new ClientError("Serialized query examples must contain name=value pairs");
        try { return [decodeURIComponent(pair.slice(0, index).replace(/\+/g, " ")), pair.slice(index + 1)]; }
        catch (_) { throw new ClientError("Serialized query names must use valid percent-encoding"); }
      }) };
    }
    throw new ClientError("This parameter cannot use serialized wire input in a browser");
  }
  function expandServer(server, variables) {
    let url = checkText(server.url || "", "Server URL", 8192);
    const definitions = new Map((server.variables || []).map(variable => [variable.name, variable]));
    url = url.replace(/\{([^{}]+)\}/g, (_, name) => {
      const variable = definitions.get(name);
      if (!variable) throw new ClientError("Unknown server variable: " + name);
      const value = own(variables, name) ? variables[name] : variable.default;
      checkText(value, "Server variable", 4096);
      if (variable.enum && variable.enum.length && !variable.enum.includes(value)) throw new ClientError("Server variable is outside its declared choices: " + name);
      return value;
    });
    if (/[{}]/.test(url)) throw new ClientError("Unresolved server variable");
    return url;
  }
  function targetURL(value, label) {
    let result;
    try { result = new root.URL(value); } catch (_) { throw new ClientError(label + " must be an absolute HTTP(S) URL"); }
    if (!["http:", "https:"].includes(result.protocol) || result.username || result.password || result.hash) throw new ClientError(label + " must be HTTP(S), without URL credentials or a fragment");
    return result;
  }
  function httpMethod(value) {
    const method = checkText(String(value || "").toUpperCase(), "HTTP method", 32);
    if (!/^[!#$%&'*+.^_`|~0-9A-Z-]+$/.test(method) || ["CONNECT", "TRACE", "TRACK"].includes(method)) throw new ClientError("This HTTP method cannot be sent by a browser");
    return method;
  }
  function buildRequest(api, operation, input = {}) {
    if (!api || api.protocol !== "http") throw new ClientError("This browser client executes HTTP operations only");
    if (operation.interaction && operation.interaction !== "request") throw new ClientError("Callbacks and webhooks describe incoming interactions and cannot be sent as API endpoints");
    operationLimits(api, operation);
    const method = httpMethod(operation.method);
    const servers = operation.servers || api.servers || [];
    const serverIndex = input.serverIndex == null ? 0 : input.serverIndex;
    if (!Number.isInteger(serverIndex) || serverIndex < 0 || (servers.length ? serverIndex >= servers.length : serverIndex !== 0)) throw new ClientError("Select a valid server");
    const selected = servers[serverIndex] || { url: "", variables: [] };
    const server = { ...selected, url: own(input, "serverURL") ? input.serverURL : selected.url };
    const expanded = expandServer(server, input.variables || {});
    let base;
    if (input.baseURL) base = targetURL(checkText(input.baseURL, "Base URL", 8192), "Base URL");
    let target;
    try { target = targetURL(expanded, "Server URL"); }
    catch (error) {
      if (/^[A-Za-z][A-Za-z0-9+.-]*:/.test(expanded) || expanded.startsWith("//") || !base) throw error;
      try { target = targetURL(new root.URL(expanded, base).href, "Resolved server URL"); }
      catch (_) { throw new ClientError("Relative server URL cannot be resolved against the explicit base URL"); }
    }
    const parameters = operation.parameters || [];
    if (parameters.length > LIMITS.parameters) throw new ClientError("Operation exceeds the browser client's parameter limit");
    let path = checkText(operation.path || "", "Operation path");
    if (!path.startsWith("/") || /[?#\r\n\\]/.test(path)) throw new ClientError("Invalid operation path");
    const query = [];
    const queryOwners = new Map();
    const headers = new Map();
    let requestHeaderBytes = 0;
    const pathSecrets = [];
    const addQuery = (name, value, owner, secret) => {
      if (queryOwners.has(name) && queryOwners.get(name) !== owner) throw new ClientError("Conflicting query parameter: " + name);
      queryOwners.set(name, owner); query.push({ name, value, secret: secret || sensitive.test(name) || (api.securitySchemes || []).some(scheme => scheme.type === "apiKey" && scheme.in === "query" && scheme.name === name) });
    };
    const addHeader = (name, value, owner, secret) => {
      const lower = headerName(name);
      if (headers.has(lower)) throw new ClientError("Conflicting request header: " + name);
      requestHeaderBytes += name.length + value.length;
      if (requestHeaderBytes > 65536) throw new ClientError("Request headers exceed the browser client's limit");
      headers.set(lower, { name, value: headerValue(value), owner, secret: secret || sensitive.test(name) || (api.securitySchemes || []).some(scheme => scheme.type === "apiKey" && scheme.in === "header" && String(scheme.name).toLowerCase() === lower) });
    };
    for (const [name, value] of target.searchParams) addQuery(name, encoded(value), "server:" + name, sensitive.test(name));
    const seen = new Set();
    for (const parameter of parameters) {
      const key = parameter.in + ":" + parameter.name;
      if (seen.has(key)) throw new ClientError("Duplicate parameter declaration: " + parameter.name);
      seen.add(key);
      const wire = own(input.serializedValues, key);
      const provided = wire || own(input.values, key);
      let value = wire ? input.serializedValues[key] : provided ? input.values[key] : undefined;
      if (ignoredHeader(parameter)) {
        if (provided) throw new ClientError("OpenAPI ignores this header parameter; use media/auth controls: " + parameter.name);
        continue;
      }
      if (value === undefined || value === null) {
        if (parameter.required || parameter.in === "path") throw new ClientError("Required parameter is missing: " + parameter.name);
        continue;
      }
      if (!wire && value === "" && parameter.in === "query" && !parameter.allowEmptyValue) throw new ClientError("This query parameter does not allow an empty value: " + parameter.name);
      if (!wire && !(parameter.content && parameter.content.length) && (Array.isArray(value) && value.length === 0 || value && typeof value === "object" && !Array.isArray(value) && Object.keys(value).length === 0)) {
        if (parameter.required || parameter.in === "path") throw new ClientError("A required collection parameter must not be empty: " + parameter.name);
        continue;
      }
      const serialized = wire ? serializedParameter(parameter, value) : serializeParameter(parameter, value);
      if (serialized.query && !serialized.query.length && parameter.required) throw new ClientError("A required query parameter must not serialize to an empty collection");
      if (serialized.query) for (const [name, entry] of serialized.query) addQuery(name, entry, key, sensitiveParameter(api, parameter));
      if (serialized.header !== undefined) addHeader(parameter.name, serialized.header, key, sensitiveParameter(api, parameter));
      if (serialized.path !== undefined) {
        const placeholder = "{" + parameter.name + "}";
        if (!path.includes(placeholder)) throw new ClientError("Path parameter has no placeholder: " + parameter.name);
        path = path.split(placeholder).join(serialized.path);
        if (sensitive.test(parameter.name) && serialized.path) pathSecrets.push(serialized.path);
      }
    }
    if (/[{}]/.test(path)) throw new ClientError("A required path placeholder is unresolved");
    if (path.split("/").some(part => /^(?:\.|%2e){1,2}$/i.test(part))) throw new ClientError("Dot path segments cannot be represented safely by browser URLs");
    const requirements = operation.security == null ? api.security || [] : operation.security;
    const choice = input.securityIndex == null ? 0 : input.securityIndex;
    if (!Number.isInteger(choice) || choice < 0 || (requirements.length ? choice >= requirements.length : choice !== 0)) throw new ClientError("Select a valid security alternative");
    const requirement = requirements.length ? requirements[choice] : {};
    const schemes = new Map((api.securitySchemes || []).map(scheme => [scheme.id, scheme]));
    for (const id of Object.keys(requirement).sort()) {
      const scheme = schemes.get(id);
      if (!scheme) throw new ClientError("Unknown security scheme: " + id);
      const credential = own(input.credentials, id) ? input.credentials[id] : {};
      const value = checkText(credential.value || "", "Required credential", 8192);
      if (scheme.type === "apiKey") {
        if (scheme.in !== "query" && scheme.in !== "header") throw new ClientError("Cookie API keys are browser-managed; this security scheme needs an external client");
        if (!value) throw new ClientError("Required API key is missing: " + id);
        if (scheme.in === "query") addQuery(scheme.name, encoded(value), "security:" + id, true);
        else if (scheme.in === "header") addHeader(scheme.name, value, "security:" + id, true);
        else throw new ClientError("Cookie API keys are browser-managed; this security scheme needs an external client");
      } else if (scheme.type === "http" && String(scheme.scheme).toLowerCase() === "basic") {
        const username = checkText(credential.username || "", "Basic username", 4096);
        const password = checkText(credential.password || "", "Basic password", 4096);
        if (!username || !password || username.includes(":")) throw new ClientError("Basic authentication needs a username without ':' and a password");
        const bytes = encoder.encode(username + ":" + password);
        const binary = Array.from(bytes, byte => String.fromCharCode(byte)).join("");
        addHeader("Authorization", "Basic " + root.btoa(binary), "security:" + id, true);
      } else if ((scheme.type === "http" && String(scheme.scheme).toLowerCase() === "bearer") || scheme.type === "oauth2" || scheme.type === "openIdConnect") {
        if (!value || /\s/.test(value)) throw new ClientError("A nonempty access token is required: " + id);
        addHeader("Authorization", "Bearer " + value, "security:" + id, true);
      } else throw new ClientError("Unsupported browser authentication scheme: " + scheme.type);
    }
    let body;
    if (operation.requestBody && operation.requestBody.required && !own(input, "body")) throw new ClientError("This operation requires a request body");
    if (own(input, "body")) {
      if (!operation.requestBody) throw new ClientError("This operation does not declare a request body");
      if (method === "GET" || method === "HEAD") throw new ClientError("Browsers cannot send a request body with GET or HEAD");
      const media = (operation.requestBody.content || []).find(item => item.name === input.mediaType);
      if (!media) throw new ClientError("Select a declared request media type");
      body = rawMedia(input.body, media.name);
      addHeader("Content-Type", media.name, "body", false);
    }
    if (input.accept) addHeader("Accept", checkText(input.accept, "Accept media type", 1024), "accept", false);
    if (input.headers != null) {
      if (!Array.isArray(input.headers) || input.headers.length > 32) throw new ClientError("Additional headers must contain at most 32 name/value pairs");
      for (const entry of input.headers) {
        if (!Array.isArray(entry) || entry.length !== 2) throw new ClientError("Additional headers must be name/value pairs");
        addHeader(checkText(entry[0], "Header name", 256), checkText(entry[1], "Header value"), "additional", false);
      }
    }
    const basePath = target.pathname.replace(/\/$/, "");
    target.pathname = basePath + path;
    target.search = query.length ? "?" + query.map(entry => encoded(entry.name) + "=" + entry.value).join("&") : "";
    if (encoder.encode(target.href).byteLength > LIMITS.url) throw new ClientError("Request URL exceeds the browser client's limit");
    let previewPath = path;
    for (const secret of pathSecrets) previewPath = previewPath.split(secret).join("[redacted]");
    const previewURL = new root.URL(target.href);
    previewURL.pathname = basePath + previewPath;
    previewURL.search = query.length ? "?" + query.map(entry => encoded(entry.name) + "=" + (entry.secret ? encoded("[redacted]") : entry.value)).join("&") : "";
    const headerObject = Object.create(null);
    const headerPreview = [];
    for (const { name, value, secret } of headers.values()) {
      headerObject[name] = value;
      headerPreview.push(name + ": " + (secret ? "[redacted]" : value));
    }
    return {
      url: target.href, method, headers: headerObject, body,
      credentials: input.includeCookies === true ? "include" : "omit",
      curlPreview: { url: previewURL.href, headers: headerPreview },
      preview: method + " " + previewURL.href + (headerPreview.length ? "\n" + headerPreview.join("\n") : "") + (body !== undefined ? "\nBody: " + encoder.encode(body).byteLength + " bytes (shown in the editor)" : ""),
    };
  }
  function shellQuote(value) { return "'" + String(value).replace(/'/g, "'\\''") + "'"; }
  function curlRequest(request, { redact = true } = {}) {
    const method = httpMethod(request.method);
    const preview = redact && request.curlPreview;
    const target = targetURL(checkText(request.url, "Request URL", LIMITS.url), "Request URL");
    if (redact && !preview) for (const name of [...target.searchParams.keys()]) if (sensitive.test(name)) target.searchParams.set(name, "[redacted]");
    const url = preview ? preview.url : target.href;
    const headers = preview ? preview.headers : Object.entries(request.headers || {}).map(([name, value]) => headerName(name) && name + ": " + (redact && sensitive.test(name) ? "[redacted]" : headerValue(value)));
    const lines = ["curl --include --request " + shellQuote(method), "  --url " + shellQuote(url)];
    for (const header of headers) lines.push("  --header " + shellQuote(header));
    if (request.body !== undefined) {
      const body = checkText(request.body, "Request body", LIMITS.body);
      // --data-raw keeps a leading @ as request text instead of reading a local file.
      lines.push("  --data-raw " + shellQuote(redact ? "[" + encoder.encode(body).byteLength + " body bytes redacted]" : body));
    }
    return lines.join(" \\\n") + (request.credentials === "include" ? "\n# Browser-managed cookies are not included in this command." : "");
  }
  function clientSettings(input = {}) {
    const timeoutMs = input.requestTimeoutMs == null ? LIMITS.timeout : Number(input.requestTimeoutMs);
    const maxBytes = input.maxResponseBytes == null ? LIMITS.response : Number(input.maxResponseBytes);
    if (!Number.isInteger(timeoutMs) || timeoutMs < 1000 || timeoutMs > LIMITS.timeout || !Number.isInteger(maxBytes) || maxBytes < 1024 || maxBytes > LIMITS.response) throw new ClientError("Invalid documentation API client limits");
    return { timeoutMs, maxBytes, showCurl: input.showCurl !== false && input.showCurl !== "false" };
  }
  function abortError(message) { const error = new ClientError(message); error.name = "AbortError"; return error; }
  function quietCancel(reader) {
    try { const pending = reader.cancel(); if (pending && pending.catch) pending.catch(() => {}); } catch (_) {}
  }
  async function readResponse(response, options = {}) {
    const maximum = options.maxBytes == null ? LIMITS.response : options.maxBytes;
    const chunkLimit = options.maxChunks == null ? LIMITS.chunks : options.maxChunks;
    if (!Number.isInteger(maximum) || maximum < 1 || maximum > LIMITS.contract || !Number.isInteger(chunkLimit) || chunkLimit < 1 || chunkLimit > LIMITS.chunks) throw new ClientError("Invalid response preview limit");
    const signal = options.signal;
    const reader = response.body && response.body.getReader ? response.body.getReader() : null;
    const onAbort = () => { if (reader) quietCancel(reader); };
    let complete = false;
    if (signal) signal.addEventListener("abort", onAbort, { once: true });
    try {
      if (signal && signal.aborted) throw abortError("Request canceled");
      const headers = [];
      let headerBytes = 0;
      if (response.headers && response.headers.forEach) response.headers.forEach((value, name) => {
        headerBytes += name.length + value.length;
        if (headers.length >= 128 || headerBytes > 65536) throw new ClientError("Response headers exceed the preview limit");
        headers.push([name, sensitive.test(name) ? "[redacted]" : value]);
      });
      if (!reader) { complete = true; return { status: response.status, statusText: response.statusText || "", headers, text: "", bytes: 0, truncated: false }; }
      const decoder = new root.TextDecoder();
      const parts = [];
      let bytes = 0;
      let chunks = 0;
      let truncated = false;
      for (;;) {
        if (signal && signal.aborted) throw abortError("Request canceled");
        const next = await reader.read();
        if (signal && signal.aborted) throw abortError("Request canceled");
        if (next.done) { complete = true; break; }
        if (++chunks > chunkLimit) { truncated = true; break; }
        const value = next.value;
        if (!(value instanceof Uint8Array)) throw new ClientError("Unsupported response stream");
        const remaining = maximum - bytes;
        const take = Math.min(remaining, value.byteLength);
        if (take) { parts.push(decoder.decode(value.subarray(0, take), { stream: true })); bytes += take; }
        if (take < value.byteLength) { truncated = true; break; }
      }
      parts.push(decoder.decode());
      return { status: response.status, statusText: response.statusText || "", headers, text: parts.join(""), bytes, truncated };
    } finally {
      if (signal) signal.removeEventListener("abort", onAbort);
      if (reader) {
        if (!complete) quietCancel(reader);
        try { reader.releaseLock(); } catch (_) {}
      }
    }
  }
  function executeRequest(request, options = {}) {
    const url = targetURL(checkText(request.url, "Request URL", LIMITS.url), "Request URL").href;
    const method = httpMethod(request.method);
    if (request.body !== undefined) {
      checkText(request.body, "Request body", LIMITS.body);
      if (method === "GET" || method === "HEAD") throw new ClientError("Browsers cannot send a request body with GET or HEAD");
    }
    const fetcher = options.fetch || root.fetch;
    const timeout = options.timeoutMs == null ? LIMITS.timeout : options.timeoutMs;
    if (!Number.isInteger(timeout) || timeout < 1 || timeout > LIMITS.timeout) throw new ClientError("Request timeout must be between 1 and 30000 ms");
    const controller = new root.AbortController();
    const external = options.signal;
    const abort = () => controller.abort("canceled");
    if (external) { external.addEventListener("abort", abort, { once: true }); if (external.aborted) abort(); }
    const schedule = options.setTimeout || root.setTimeout;
    const unschedule = options.clearTimeout || root.clearTimeout;
    const timer = schedule(() => controller.abort("timeout"), timeout);
    const promise = (async () => {
      try {
        if (controller.signal.aborted) throw abortError("Request canceled");
        const response = await fetcher(url, { method, headers: request.headers, body: request.body, signal: controller.signal, credentials: request.credentials || "omit", redirect: "error", referrerPolicy: "no-referrer", cache: "no-store" });
        return await readResponse(response, { maxBytes: options.maxBytes, signal: controller.signal });
      } catch (error) {
        controller.abort(controller.signal.reason || "failed");
        if (controller.signal.reason === "timeout") throw abortError("Request timed out after " + timeout + " ms");
        if (controller.signal.reason === "canceled") throw abortError("Request canceled");
        if (error instanceof ClientError) throw error;
        throw new ClientError("Request failed. Check the target, network and CORS permissions. Redirects are not followed.");
      } finally {
        unschedule(timer);
        if (external) external.removeEventListener("abort", abort);
      }
    })();
    return { promise, cancel: abort, signal: controller.signal };
  }
  const attachments = new WeakMap();
  function attach(document, environment = {}) {
    if (attachments.has(document)) return attachments.get(document);
    const fetcher = environment.fetch || root.fetch.bind(root);
    const data = document.currentScript && document.currentScript.dataset || {};
    const settings = clientSettings(environment.settings || { requestTimeoutMs: data.apiTimeout, maxResponseBytes: data.apiResponseBytes, showCurl: data.apiShowCurl });
    const buttons = Array.from(document.querySelectorAll("[data-api-operation]"));
    if (!buttons.length) return () => {};
    const remove = [];
    let active = null;
    let generation = 0;
    let cachedURL = "";
    let cachedContract = null;
    let contractJob = null;
    const element = (tag, text, className) => { const item = document.createElement(tag); if (text !== undefined) item.textContent = text; if (className) item.className = className; return item; };
    const listen = (node, name, callback, bag = remove) => { node.addEventListener(name, callback); bag.push(() => node.removeEventListener(name, callback)); };
    function closeActive() {
      generation++;
      if (contractJob) { contractJob.cancel(); contractJob = null; }
      if (active) { active.dispose(); active = null; }
    }
    async function contract(url) {
      if (cachedURL === url && cachedContract) return cachedContract;
      if (contractJob) contractJob.cancel();
      cachedURL = url; cachedContract = null;
      const job = executeRequest({ url, method: "GET", headers: {}, credentials: "same-origin" }, { fetch: fetcher, maxBytes: LIMITS.contract });
      contractJob = job;
      try {
        const response = await job.promise;
        if (response.status < 200 || response.status >= 300 || response.truncated) throw new ClientError("Contract exceeds its size limit or could not be loaded");
        let parsed;
        try { parsed = JSON.parse(response.text); } catch (_) { throw new ClientError("The documentation API contract is invalid JSON"); }
        const api = parsed.api || parsed;
        if (!api || api.protocol !== "http" || !Array.isArray(api.operations)) throw new ClientError("This operation has no HTTP client contract");
        if (cachedURL === url) cachedContract = api;
        return api;
      } finally { if (contractJob === job) contractJob = null; }
    }
    function showPanel(button, host, api, operation) {
      const listeners = [];
      const secretInputs = [];
      const form = element("form", undefined, "api-client-form");
      form.noValidate = true;
      const status = element("p", "Review the target and request, then choose Send.", "api-client-status");
      status.setAttribute("role", "status");
      const preview = element("pre", "", "api-client-preview");
      const curl = settings.showCurl ? element("pre", "", "api-client-curl") : null;
      if (curl) curl.setAttribute("aria-label", "cURL preview with credentials and body redacted");
      const response = element("pre", "", "api-client-response");
      response.setAttribute("aria-label", "Response preview");
      let requestJob = null;
      let previewTimer = null;
      let requestGeneration = 0;
      const fields = [];
      function field(label, control, secret = false) {
        const wrapper = element("label", undefined, "api-client-field");
        wrapper.append(element("span", label), control); form.append(wrapper); fields.push(control);
        if (secret) secretInputs.push(control);
        return control;
      }
      function input(label, value = "", secret = false) {
        const control = element("input"); control.type = secret ? "password" : "text";
        control.value = value; control.maxLength = secret ? 8192 : LIMITS.input;
        control.autocomplete = "off"; control.spellcheck = false;
        return field(label, control, secret);
      }
      function select(label, values) {
        const control = element("select");
        values.forEach(([value, text]) => { const option = element("option", text); option.value = String(value); control.append(option); });
        if (values.length) control.value = String(values[0][0]);
        return field(label, control);
      }
      const servers = operation.servers || api.servers || [];
      const serverChoice = select("Server", servers.length ? servers.map((server, index) => [index, server.description || server.url]) : [[0, "Enter a server URL"]]);
      const serverURL = input("Target server URL", servers[0] ? servers[0].url : "");
      const baseURL = input("Contract/base URL for relative servers (enter explicitly)");
      const variableContainer = element("div"); form.append(variableContainer);
      let variableInputs = new Map();
      function updateServer() {
        variableContainer.replaceChildren(); variableInputs = new Map();
        const server = servers[Number(serverChoice.value)];
        if (server) serverURL.value = server.url;
        for (const variable of server && server.variables || []) {
          const label = element("label", undefined, "api-client-field");
          const control = element("input"); control.type = "text"; control.value = variable.default || ""; control.maxLength = 4096; control.autocomplete = "off";
          label.append(element("span", "Server variable: " + variable.name), control); variableContainer.append(label); variableInputs.set(variable.name, control);
        }
      }
      updateServer();
      listen(serverChoice, "change", updateServer, listeners);
      const schemaIndex = new Map((api.schemas || []).map(schema => [schema.id, schema]));
      const parameterInputs = new Map();
      for (const parameter of operation.parameters || []) {
        if (ignoredHeader(parameter)) continue;
        const label = parameter.in + " · " + parameter.name + (parameter.required ? " (required)" : "") + " — scalar or JSON array/object";
        const control = input(label, "", sensitiveParameter(api, parameter));
        const sample = parameterSample(api, parameter, schemaIndex);
        const usable = sample && encoder.encode(sample.value).byteLength <= LIMITS.input;
        if (usable) control.value = sample.value;
        else if (sample) control.placeholder = "Example exceeds the input limit; enter a smaller value";
        let wire;
        if (["path", "query", "header"].includes(parameter.in)) {
          wire = element("input"); wire.type = "checkbox"; wire.checked = usable && sample.serialized === true;
          field("Already serialized wire value for " + parameter.name, wire);
        }
        parameterInputs.set(parameter.in + ":" + parameter.name, { parameter, control, wire });
      }
      const requirements = operation.security == null ? api.security || [] : operation.security;
      const securityChoice = select("Authentication alternative", requirements.length ? requirements.map((requirement, index) => [index, Object.keys(requirement).length ? Object.keys(requirement).join(" + ") : "No authentication"]) : [[0, "No authentication"]]);
      const authContainer = element("div", undefined, "api-client-credentials"); form.append(authContainer);
      let authInputs = new Map();
      function updateAuth() {
        for (const controls of authInputs.values()) for (const control of Object.values(controls)) control.value = "";
        authContainer.replaceChildren(); authInputs = new Map();
        const requirement = requirements[Number(securityChoice.value)] || {};
        for (const id of Object.keys(requirement).sort()) {
          const scheme = (api.securitySchemes || []).find(item => item.id === id);
          const controls = {};
          const names = scheme && scheme.type === "http" && String(scheme.scheme).toLowerCase() === "basic" ? ["username", "password"] : ["value"];
          for (const name of names) {
            const wrapper = element("label", undefined, "api-client-field");
            const control = element("input"); control.type = "password"; control.maxLength = 8192; control.autocomplete = "off";
            const scopes = requirement[id] && requirement[id].length ? " · scopes: " + requirement[id].join(", ") : "";
            wrapper.append(element("span", id + " " + (name === "value" ? "key/token" : name) + scopes), control); authContainer.append(wrapper); controls[name] = control;
          }
          authInputs.set(id, controls);
        }
      }
      updateAuth(); listen(securityChoice, "change", updateAuth, listeners);
      const bodyDefinition = operation.requestBody;
      let bodyEnabled, mediaType, body;
      if (bodyDefinition) {
        bodyEnabled = element("input"); bodyEnabled.type = "checkbox"; bodyEnabled.checked = bodyDefinition.required === true; bodyEnabled.disabled = bodyDefinition.required === true; field("Send request body", bodyEnabled);
        mediaType = select("Request media type", (bodyDefinition.content || []).map(media => [media.name, media.name]));
        body = element("textarea"); body.rows = 10; body.maxLength = LIMITS.body; body.autocomplete = "off"; body.spellcheck = false; body.placeholder = "Paste the serialized body. JSON bytes and large integers are preserved."; field("Raw request body", body);
        function fillBodyExample() {
          const media = (bodyDefinition.content || []).find(item => item.name === mediaType.value);
          const sample = media && exampleSample(media, media.name, schemaIndex);
          if (sample && encoder.encode(sample.value).byteLength <= LIMITS.body) body.value = sample.value;
          else { body.value = ""; if (sample) body.placeholder = "Example exceeds the body limit; enter a smaller serialized body"; }
        }
        fillBodyExample(); listen(mediaType, "change", fillBodyExample, listeners);
      }
      const responseTypes = [...new Set((operation.responses || []).flatMap(item => (item.content || []).map(media => media.name)))];
      const accept = select("Accept response media type", [["", "Server default"], ...responseTypes.map(name => [name, name])]);
      const additionalHeaders = element("textarea"); additionalHeaders.rows = 3; additionalHeaders.maxLength = 16384; additionalHeaders.autocomplete = "off"; additionalHeaders.spellcheck = false; additionalHeaders.placeholder = "X-Request-ID: example"; field("Additional headers (one Name: value per line, at most 32)", additionalHeaders);
      const cookies = element("input"); cookies.type = "checkbox"; field("Include browser-managed cookies (off by default)", cookies);
      const actions = element("div", undefined, "api-client-actions");
      const send = element("button", "Send"); send.type = "submit";
      const cancel = element("button", "Cancel"); cancel.type = "button"; cancel.disabled = true;
      const close = element("button", "Close and clear"); close.type = "button";
      actions.append(send, cancel, close);
      if (curl) {
        const copyCurl = element("button", "Copy cURL with current values"); copyCurl.type = "button";
        listen(copyCurl, "click", async () => {
          try {
            if (!root.navigator || !root.navigator.clipboard || !root.isSecureContext) throw new ClientError("Clipboard access requires a secure page");
            const command = curlRequest(collect(), { redact: false });
            await root.navigator.clipboard.writeText(command);
            status.textContent = "cURL copied with the current request values. Keep credentials private.";
          } catch (error) { status.textContent = error.message || "Clipboard unavailable"; }
        }, listeners);
        actions.append(copyCurl);
      }
      form.append(preview);
      if (curl) form.append(element("p", "cURL preview redacts credentials and body. Copying uses the values you entered, including credentials.", "api-client-help"), curl);
      form.append(actions, status, response);
      host.replaceChildren(form); host.hidden = false; host.className = "api-client";
      function collect() {
        const values = Object.create(null);
        const serializedValues = Object.create(null);
        for (const [key, { parameter, control, wire }] of parameterInputs) {
          if (wire && wire.checked) serializedValues[key] = control.value;
          else if (control.value !== "" || parameter.allowEmptyValue) values[key] = parseParameterInput(control.value, parameter);
        }
        const credentials = Object.create(null);
        for (const [id, controls] of authInputs) credentials[id] = Object.fromEntries(Object.entries(controls).map(([name, control]) => [name, control.value]));
        const inputValues = { serverIndex: Number(serverChoice.value), serverURL: serverURL.value, baseURL: baseURL.value, variables: Object.fromEntries([...variableInputs].map(([name, control]) => [name, control.value])), values, serializedValues, credentials, securityIndex: Number(securityChoice.value), includeCookies: cookies.checked, accept: accept.value };
        const headerLines = checkText(additionalHeaders.value, "Additional headers").split(/\r?\n/).filter(line => line.trim() !== "");
        if (headerLines.length > 32) throw new ClientError("Use at most 32 additional headers");
        inputValues.headers = headerLines.map(line => {
          const separator = line.indexOf(":");
          if (separator < 1) throw new ClientError("Write each additional header as Name: value");
          return [line.slice(0, separator).trim(), line.slice(separator + 1).trim()];
        });
        if (bodyEnabled && bodyEnabled.checked) { inputValues.body = body.value; inputValues.mediaType = mediaType.value; }
        return buildRequest(api, operation, inputValues);
      }
      function refresh() {
        baseURL.parentElement.hidden = /^[A-Za-z][A-Za-z0-9+.-]*:/.test(serverURL.value);
        try { const request = collect(); preview.textContent = request.preview; if (curl) curl.textContent = curlRequest(request); }
        catch (error) { preview.textContent = error.message; if (curl) curl.textContent = ""; }
      }
      listen(form, "input", () => {
        if (previewTimer !== null) root.clearTimeout(previewTimer);
        previewTimer = root.setTimeout(() => { previewTimer = null; refresh(); }, 80);
      }, listeners);
      listen(form, "change", refresh, listeners);
      listen(form, "submit", async event => {
        event.preventDefault();
        if (previewTimer !== null) { root.clearTimeout(previewTimer); previewTimer = null; }
        let request;
        try { request = collect(); preview.textContent = request.preview; if (curl) curl.textContent = curlRequest(request); }
        catch (error) { status.textContent = error.message; return; }
        if (requestJob) requestJob.cancel();
        const run = ++requestGeneration;
        requestJob = executeRequest(request, { fetch: fetcher, timeoutMs: settings.timeoutMs, maxBytes: settings.maxBytes });
        send.disabled = true; cancel.disabled = false; status.textContent = "Sending…"; response.textContent = "";
        try {
          const result = await requestJob.promise;
          if (run !== requestGeneration) return;
          status.textContent = "HTTP " + result.status + (result.truncated ? " · preview truncated at its byte/chunk limit" : "");
          response.textContent = result.headers.map(([name, value]) => name + ": " + value).join("\n") + "\n\n" + result.text;
        } catch (error) { if (run === requestGeneration) status.textContent = error.message; }
        finally { if (run === requestGeneration) { requestJob = null; send.disabled = false; cancel.disabled = true; } }
      }, listeners);
      listen(cancel, "click", () => { if (requestJob) requestJob.cancel(); }, listeners);
      listen(close, "click", closeActive, listeners);
      refresh();
      const state = { dispose() {
        requestGeneration++;
        if (previewTimer !== null) { root.clearTimeout(previewTimer); previewTimer = null; }
        if (requestJob) requestJob.cancel(); requestJob = null;
        for (const control of fields) if ("value" in control) control.value = "";
        for (const control of secretInputs) control.value = "";
        for (const controls of authInputs.values()) for (const control of Object.values(controls)) control.value = "";
        for (const removeListener of listeners) removeListener();
        host.replaceChildren(); host.hidden = true; button.disabled = false;
      } };
      return state;
    }
    for (const button of buttons) {
      const operationContainer = button.closest(".api-operation");
      const host = operationContainer && operationContainer.querySelector("[data-api-client]");
      if (!host) continue;
      listen(button, "click", async () => {
        closeActive(); const run = generation;
        const cancelLoad = element("button", "Cancel loading"); cancelLoad.type = "button";
        host.hidden = false; host.replaceChildren(element("p", "Loading request contract…", "api-client-status"), cancelLoad);
        cancelLoad.addEventListener("click", closeActive, { once: true });
        button.disabled = true;
        active = { dispose() { host.replaceChildren(); host.hidden = true; button.disabled = false; } };
        try {
          const url = new root.URL(button.dataset.apiUrl, document.baseURI);
          const page = new root.URL(document.baseURI);
          if (url.origin !== page.origin || !["http:", "https:"].includes(url.protocol) || url.username || url.password || url.hash) throw new ClientError("Serve this documentation over HTTP(S) to load its local API contract");
          const api = await contract(url.href);
          if (run !== generation) return;
          const operation = api.operations.find(item => item.id === button.dataset.apiOperation);
          if (!operation) throw new ClientError("Operation was not found in the API contract");
          operationLimits(api, operation);
          if (operation.interaction && operation.interaction !== "request") throw new ClientError("Callbacks and webhooks describe incoming interactions and cannot be sent as API endpoints");
          active = showPanel(button, host, api, operation);
        } catch (error) {
          if (run === generation) { host.replaceChildren(element("p", error.message, "api-client-status")); button.disabled = false; }
        }
      });
    }
    const onHide = () => { closeActive(); if (contractJob) contractJob.cancel(); contractJob = null; cachedContract = null; cachedURL = ""; };
    if (root.addEventListener) listen(root, "pagehide", onHide);
    const dispose = () => { onHide(); for (const removeListener of remove) removeListener(); attachments.delete(document); };
    attachments.set(document, dispose);
    return dispose;
  }
  return Object.freeze({ LIMITS, buildRequest, serializeParameter, parseParameterInput, curlRequest, clientSettings, readResponse, executeRequest, attach });
});
