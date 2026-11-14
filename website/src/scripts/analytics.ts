// PostHog counts what the site gets: pageviews, download clicks, and the
// links that leave it. It runs only when a project key is set at build time,
// so a local build sends nothing.
//
// PostHog runs without person profiles, cookies, recordings, autocapture or
// flags. A link marked data-ph names its own event; any other link to another
// site is an outbound click. The data-ph-* values become the event's
// properties, so the markup says what a click means.
//
// The slim bundle leaves out the extensions, which is most of the library,
// so the pageview is sent here rather than by PostHog's own pageview code.
import posthog from "posthog-js/dist/module.slim";

const key = import.meta.env.PUBLIC_POSTHOG_KEY;

if (key) {
  posthog.init(key, {
    api_host: import.meta.env.PUBLIC_POSTHOG_HOST || "https://us.i.posthog.com",
    person_profiles: "never",
    persistence: "localStorage",
    autocapture: false,
    capture_pageview: false,
    disable_session_recording: true,
  });
  posthog.capture("$pageview");

  document.addEventListener("click", (event) => {
    const target = event.target;
    if (!(target instanceof Element)) return;
    const link = target.closest("a");
    if (!(link instanceof HTMLAnchorElement)) return;
    const url = new URL(link.href, location.href);
    const name = link.dataset.ph;
    if (!name && url.origin === location.origin) return;
    const properties: Record<string, string> = { url: url.href, host: url.hostname };
    for (const [property, value] of Object.entries(link.dataset)) {
      if (property.startsWith("ph") && property !== "ph") {
        properties[snakeCase(property.slice(2))] = value;
      }
    }
    posthog.capture(name ?? "outbound_click", properties);
  });
}

/** Turns the rest of a data-ph-* name into a snake_case property. */
function snakeCase(name: string): string {
  return name.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`).replace(/^_/, "");
}
