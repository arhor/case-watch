import { readdirSync } from "node:fs";
import { spawnSync } from "node:child_process";
for (const dir of ["src", "test", "scripts"]) {
  for (const name of readdirSync(dir).filter((n) => n.endsWith(".js"))) {
    const result = spawnSync(process.execPath, ["--check", `${dir}/${name}`], {
      stdio: "inherit",
    });
    if (result.status !== 0) process.exit(1);
  }
}
