// Theme toggle, install tabs and copy. The page works without this file.
(() => {
  const root = document.documentElement;
  const themeButton = document.querySelector(".theme");
  const systemDark = window.matchMedia("(prefers-color-scheme: dark)");
  const current = () => root.dataset.theme || (systemDark.matches ? "dark" : "light");
  const labelTheme = () => {
    const next = current() === "dark" ? "light" : "dark";
    themeButton.querySelector("span").textContent = next;
    themeButton.setAttribute("aria-label", `Switch to ${next} theme`);
  };
  if (themeButton) {
    themeButton.hidden = false;
    labelTheme();
    systemDark.addEventListener("change", labelTheme);
    themeButton.addEventListener("click", () => {
      root.dataset.theme = current() === "dark" ? "light" : "dark";
      try { localStorage.setItem("beam-theme", root.dataset.theme); } catch {}
      labelTheme();
    });
  }

  const tabs = [...document.querySelectorAll(".tab")];
  const copyButton = document.querySelector(".copy");
  let timer;
  const select = (tab) => {
    for (const t of tabs) {
      const on = t === tab;
      t.setAttribute("aria-selected", String(on));
      t.tabIndex = on ? 0 : -1;
      document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
    }
    copyButton.textContent = "copy";
  };
  tabs.forEach((tab, i) => {
    tab.addEventListener("click", () => select(tab));
    tab.addEventListener("keydown", (event) => {
      const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
      if (!step) return;
      const next = tabs[(i + step + tabs.length) % tabs.length];
      select(next);
      next.focus();
    });
  });
  copyButton?.addEventListener("click", async () => {
    const panel = document.querySelector(".cmd p:not([hidden])");
    let label = "copied";
    try {
      await navigator.clipboard.writeText(panel.textContent.trim());
    } catch {
      // Clipboard can be blocked; select the text so the reader can copy it.
      getSelection().selectAllChildren(panel);
      label = "select it";
    }
    copyButton.textContent = label;
    clearTimeout(timer);
    timer = setTimeout(() => { copyButton.textContent = "copy"; }, 1800);
  });
})();
