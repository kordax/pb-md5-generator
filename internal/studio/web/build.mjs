import { build } from "esbuild";
import { readFile, writeFile, mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { dirname, relative, join, resolve } from "node:path";

const root = fileURLToPath(new URL(".", import.meta.url));
const result = await build({ absWorkingDir: root, entryPoints: ["src/editor.mjs"], outfile: "assets/editor.js", bundle: true, format: "esm", platform: "browser", target: ["es2022"], minify: true, legalComments: "external", charset: "utf8", write: false, metafile: true });
const packages = new Map();
for (const input of Object.keys(result.metafile.inputs)) {
  if (!input.includes("node_modules/")) continue;
  let directory = dirname(resolve(root, input));
  while (directory.startsWith(root)) {
    let metadata;
    try { metadata = JSON.parse(await readFile(join(directory, "package.json"), "utf8")); }
    catch (error) { if (error.code !== "ENOENT") throw error; directory = dirname(directory); continue; }
    const id = metadata.name + "@" + metadata.version;
    if (!packages.has(id)) {
      let license;
      for (const name of ["LICENSE", "LICENSE.md", "LICENSE.txt", "license", "license.md", "license.txt"]) {
        try { license = await readFile(join(directory, name), "utf8"); break; }
        catch (error) { if (error.code !== "ENOENT") throw error; }
      }
      if (!license) throw new Error("Missing bundled dependency license: " + id);
      packages.set(id, license.trim());
    }
    break;
  }
}
const notices = "Bundled editor dependency notices\n\n" + [...packages.keys()].sort().map(id => id + "\n\n" + packages.get(id)).join("\n\n========================================\n\n") + "\n";
const outputs = [...result.outputFiles, { path: join(root, "assets/THIRD_PARTY_NOTICES.txt"), contents: Buffer.from(notices) }];
let changed = false;
for (const file of outputs) {
  if (process.argv.includes("--check")) {
    let actual;
    try { actual = await readFile(file.path); } catch (error) { if (error.code !== "ENOENT") throw error; actual = null; }
    if (!actual?.equals(Buffer.from(file.contents))) { console.error("Editor asset is stale: " + relative(root, file.path)); changed = true; }
  } else {
    await mkdir(dirname(file.path), { recursive: true });
    await writeFile(file.path, file.contents);
  }
}
if (changed) process.exitCode = 1;
