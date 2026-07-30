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

    // Validate the token is still alive by calling the greytHR API.
    showStatus("Validating session...", true);
    const validateResp = await fetch(`https://${host}/v3/api/dashboard/dashlet/payslip`, {
      headers: {
        "Cookie": `access_token=${cookie.value}`,
        "accept": "application/json",
        "x-requested-with": "XMLHttpRequest",
      },
    });
    if (validateResp.status === 401) {
      throw new Error("Your greytHR session has expired. Refresh the greytHR page and try again.");
    }
    if (!validateResp.ok) {
      throw new Error(`greytHR returned ${validateResp.status}. Try again.`);
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

    if (!profileId) {
      throw new Error("Could not find your profile ID. Navigate to the Payslips page in greytHR first, then try again.");
    }

    // Send to Cresto — auto-saves the session and redirects.
    showStatus("Connecting to Cresto...", true);
    const params = new URLSearchParams({
      host: host,
      access_token: cookie.value,
      profile_id: profileId,
    });

    await chrome.tabs.create({
      url: `${CRESTO_URL}/greythr/connect?${params.toString()}`,
    });

    showStatus("Connected! Redirecting...", true);
    setTimeout(() => window.close(), 1500);
  } catch (err) {
    showStatus(err.message, false);
    btn.disabled = false;
  }
});

// Check if Cresto is running and if the active tab is a greytHR page.
(function checkState() {
  var crestoOk = false;
  var greythrOk = false;

  function recheck() {
    if (!crestoOk) return;
    if (!greythrOk) {
      showStatus("Open your greytHR ESS portal first.", false);
      btn.disabled = true;
    } else {
      btn.disabled = false;
    }
  }

  // Check Cresto.
  fetch(CRESTO_URL + "/", { mode: "no-cors" })
    .then(() => { crestoOk = true; recheck(); })
    .catch(() => {
      showStatus("Cresto is not running. Start it with `cresto start`.", false);
      btn.disabled = true;
    });

  // Check active tab.
  chrome.tabs.query({ active: true, currentWindow: true }, (tabs) => {
    greythrOk = !!(tabs[0] && tabs[0].url && tabs[0].url.includes("greythr.com"));
    recheck();
  });
})();
