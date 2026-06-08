#!/usr/bin/env node
/*
 * render.js — the design engine's HTML -> ProRes/mp4 renderer.
 *
 * The cloud-side port of the Mac html-to-mov harness. Loads an authored HTML
 * graphic in headless Chromium, captures it frame by frame, and encodes to
 * video with ffmpeg. Baked into the workspace image at /opt/render and invoked
 * by the render-html-to-prores skill THROUGH media_job (so the run is tracked
 * and the output lands in the Media tab + Library). It is a deterministic
 * mechanic — judgment about WHAT to design lives in the design skill, not here.
 *
 * Two capture modes (see the Mac harness rationale):
 *   virtual-time (default) — pause the Web Animations timeline and scrub
 *     currentTime per frame. Deterministic + CPU-cheap; correct for CSS
 *     @keyframes animations (the bulk of the component catalog).
 *   --realtime — let the page play in wall-clock time and grab a frame every
 *     1000/fps ms. Needed for compositor-thread transitions (transform/opacity
 *     transitions, JS-state timelines) that virtual time snaps.
 *
 * Usage:
 *   node render.js --html <path> --out <path.mp4|.mov> [options]
 * Options:
 *   --duration <s>      total seconds to render            (default 5)
 *   --fps <n>           frames per second                  (default 30)
 *   --width <px>        stage width                        (default 1920)
 *   --height <px>       stage height                       (default 1080)
 *   --card <id>         add .playing to [data-card="<id>"] (default: all .card)
 *   --realtime          wall-clock capture (transitions)   (default off)
 *   --transparent       alpha output (ProRes 4444 / overlays)
 *   --codec <h264|prores>  force codec (else inferred from --out + --transparent)
 *
 * Exits non-zero with a message on any failure so media_job fails loud.
 */

const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawnSync } = require("child_process");
const puppeteer = require("puppeteer-core");

function parseArgs(argv) {
  const a = { duration: 5, fps: 30, width: 1920, height: 1080, realtime: false, transparent: false };
  for (let i = 2; i < argv.length; i++) {
    const k = argv[i];
    const next = () => argv[++i];
    switch (k) {
      case "--html": a.html = next(); break;
      case "--out": a.out = next(); break;
      case "--duration": a.duration = parseFloat(next()); break;
      case "--fps": a.fps = parseInt(next(), 10); break;
      case "--width": a.width = parseInt(next(), 10); break;
      case "--height": a.height = parseInt(next(), 10); break;
      case "--card": a.card = next(); break;
      case "--realtime": a.realtime = true; break;
      case "--transparent": a.transparent = true; break;
      case "--codec": a.codec = next(); break;
      default: break;
    }
  }
  return a;
}

function die(msg) {
  process.stderr.write(`render: ${msg}\n`);
  process.exit(1);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function main() {
  const a = parseArgs(process.argv);
  if (!a.html || !a.out) die("--html and --out are required");
  if (!fs.existsSync(a.html)) die(`html not found: ${a.html}`);
  if (!(a.fps > 0) || !(a.duration > 0)) die("--fps and --duration must be positive");

  const codec = a.codec || (a.transparent || a.out.endsWith(".mov") ? "prores" : "h264");
  const execPath = process.env.PUPPETEER_EXECUTABLE_PATH || "/usr/bin/chromium";
  if (!fs.existsSync(execPath)) die(`chromium not found at ${execPath} (set PUPPETEER_EXECUTABLE_PATH)`);

  const frameDir = fs.mkdtempSync(path.join(os.tmpdir(), "render-"));
  const totalFrames = Math.max(1, Math.round(a.duration * a.fps));

  const args = [
    "--no-sandbox",
    "--disable-setuid-sandbox",
    "--disable-dev-shm-usage",
    "--hide-scrollbars",
    "--force-color-profile=srgb",
  ];
  if (a.transparent) args.push("--default-background-color=00000000");

  let browser;
  try {
    browser = await puppeteer.launch({ executablePath: execPath, headless: "new", args });
    const page = await browser.newPage();
    await page.setViewport({ width: a.width, height: a.height, deviceScaleFactor: 1 });
    await page.goto("file://" + path.resolve(a.html), { waitUntil: "networkidle0", timeout: 60000 });

    // Enter "record mode" + start the target card. Best-effort: the design
    // scaffold uses .record-mode (chrome off, bg transparent) and .card.playing.
    await page.evaluate((cardId) => {
      document.querySelectorAll(".app, .stage, body").forEach((el) => el.classList.add("record-mode"));
      const cards = cardId
        ? document.querySelectorAll(`[data-card="${cardId}"]`)
        : document.querySelectorAll(".card");
      if (cards.length) cards.forEach((c) => c.classList.add("playing"));
      else document.body.classList.add("playing"); // graphics without a .card system
    }, a.card || "");

    // Let layout + fonts settle before the first frame.
    await sleep(250);

    for (let i = 0; i < totalFrames; i++) {
      const tMs = (i / a.fps) * 1000;
      if (a.realtime) {
        // Wall-clock: the page animates naturally; just pace the grabs.
        if (i > 0) await sleep(1000 / a.fps);
      } else {
        // Virtual-time scrub of the Web Animations timeline.
        await page.evaluate((t) => {
          for (const anim of document.getAnimations()) {
            try { anim.pause(); anim.currentTime = t; } catch (e) { /* finished/idle */ }
          }
        }, tMs);
      }
      const file = path.join(frameDir, String(i).padStart(6, "0") + ".png");
      await page.screenshot({ path: file, omitBackground: !!a.transparent });
    }
    await browser.close();
    browser = null;

    fs.mkdirSync(path.dirname(path.resolve(a.out)), { recursive: true });

    const inPattern = path.join(frameDir, "%06d.png");
    let ff;
    if (codec === "prores") {
      ff = ["-y", "-framerate", String(a.fps), "-i", inPattern,
        "-c:v", "prores_ks", "-profile:v", a.transparent ? "4" : "3",
        "-pix_fmt", a.transparent ? "yuva444p10le" : "yuv422p10le",
        "-vendor", "apl0", a.out];
    } else {
      ff = ["-y", "-framerate", String(a.fps), "-i", inPattern,
        "-c:v", "libx264", "-pix_fmt", "yuv420p", "-movflags", "+faststart", a.out];
    }
    const res = spawnSync("ffmpeg", ff, { encoding: "utf8" });
    if (res.status !== 0) {
      die(`ffmpeg failed (${res.status}): ${(res.stderr || "").split("\n").slice(-8).join("\n")}`);
    }

    fs.rmSync(frameDir, { recursive: true, force: true });
    process.stdout.write(JSON.stringify({
      ok: true, out: path.resolve(a.out), frames: totalFrames, fps: a.fps,
      codec, mode: a.realtime ? "realtime" : "virtual-time",
    }) + "\n");
  } catch (err) {
    if (browser) { try { await browser.close(); } catch (e) {} }
    try { fs.rmSync(frameDir, { recursive: true, force: true }); } catch (e) {}
    die(String((err && err.message) || err));
  }
}

main();
