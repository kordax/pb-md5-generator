### Preview requirements

Install Docker with the Compose plugin to run the container preview. Keep port
8080 available, or set `DOCS_PORT` before starting it. The [deployment guide](../deployment.md#docker)
describes ports, custom sources and transferring a built image to another host.

The published site serves static files. Readers do not need Node.js, Go or a
database on the host that serves those files.
