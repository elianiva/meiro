const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** Adds `in` to each element the first time it scrolls into view, then calls `onEnter`. */
export function arrive(selector: string, onEnter?: (el: Element) => void, margin = "0px 0px -10% 0px") {
  const nodes = document.querySelectorAll(selector);
  if (reduce || !("IntersectionObserver" in window)) {
    nodes.forEach((el) => {
      el.classList.add("in");
      onEnter?.(el);
    });
    return;
  }
  const seen = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        entry.target.classList.add("in");
        onEnter?.(entry.target);
        seen.unobserve(entry.target);
      }
    },
    { rootMargin: margin, threshold: 0.12 },
  );
  nodes.forEach((el) => seen.observe(el));
}
