const CRESTO_URL = "http://localhost:7777";

const btn = document.getElementById("connect");
const status = document.getElementById("status");

function showStatus(msg, ok) {
  status.textContent = msg;
  status.className = "status " + (ok ? "ok" : "err");
}

btn.addEventListener("click", async () => {
  btn.disabled = true;
  showStatus("Reading greytHR session...", true);

  try {
    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (!tab || !tab.url || !tab.url.includes("greythr.com")) {
      throw new Error("Open a greytHR ESS page first.");
    }

    const host = new URL(tab.url).hostname;

    // Read the httpOnly access_token cookie — extensions can do this.
    const cookie = await chrome.cookies.get({
      url: `https://${host}`,
      name: "access_token",
    });
    if (!cookie) {
      throw new Error("No access_token cookie found. Log into greytHR first.");
    }

    // Extract profile_id from the page's performance entries (API calls
    // contain the employee ID in the URL path).
    let profileId = "";
    try {
      const results = await chrome.scripting.executeScript({
        target: { tabId: tab.id },
        func: () => {
          const entries = performance.getEntriesByType("resource");
          for (const e of entries) {
            const m = e.name.match(/\/v3\/api\/(?:payroll\/(?:months|payslip)\/|notifications\/)(\d+)/);
            if (m) return m[1];
          }
          return "";
        },
      });
      profileId = results?.[0]?.result || "";
    } catch (_) {}

    // Build the Cresto connect URL with all params.
    const params = new URLSearchParams({
      host: host,
      access_token: cookie.value,
    });
    if (profileId) params.set("profile_id", profileId);

    // Send to Cresto — opens the connect confirmation page.
    await chrome.tabs.create({
      url: `${CRESTO_URL}/greythr/connect?${params.toString()}`,
    });

    showStatus("Opening Cresto...", true);
    setTimeout(() => window.close(), 1500);
  } catch (err) {
    showStatus(err.message, false);
    btn.disabled = false;
  }
});
