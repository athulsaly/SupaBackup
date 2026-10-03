// SupaBackup Dashboard Frontend Logic
let currentJobForDetails = "";
const csrfToken = document.querySelector('meta[name="csrf-token"]')?.getAttribute('content') || "";

function showTab(tabId) {
  document.querySelectorAll('.tab-content').forEach(el => el.style.display = 'none');
  document.querySelectorAll('.tab-btn').forEach(btn => btn.classList.remove('active'));

  const activeContent = document.getElementById(tabId);
  if (activeContent) activeContent.style.display = 'block';

  const activeBtn = document.querySelector(`[data-tab="${tabId}"]`);
  if (activeBtn) activeBtn.classList.add('active');

  if (tabId === 'tab-jobs') loadJobs();
  if (tabId === 'tab-config') loadConfig();
  if (tabId === 'tab-details' && currentJobForDetails) selectJobDetails(currentJobForDetails);
}

async function loadJobs() {
  try {
    const res = await fetch('/api/jobs');
    if (res.status === 401) { window.location.href = '/login'; return; }
    const jobs = await res.json();

    const tbody = document.getElementById('jobs-tbody');
    if (!tbody) return;
    tbody.innerHTML = '';

    if (!jobs || jobs.length === 0) {
      tbody.innerHTML = '<tr><td colspan="7" style="text-align:center; color: var(--text-muted);">No backup jobs configured.</td></tr>';
      return;
    }

    jobs.forEach(job => {
      const tr = document.createElement('tr');
      const statusClass = `badge-${job.last_status || 'pending'}`;
      const lastRun = job.last_run_time ? new Date(job.last_run_time).toLocaleString() : 'Never';
      const nextRun = job.next_run_time ? new Date(job.next_run_time).toLocaleString() : 'N/A';
      const duration = job.last_duration_ms ? `${(job.last_duration_ms / 1000).toFixed(1)}s` : '-';
      const size = job.last_file_size ? formatBytes(job.last_file_size) : '-';

      tr.innerHTML = `
        <td>
          <a href="javascript:void(0)" onclick="openJobDetails('${job.name}')" style="color:#60a5fa; font-weight:600; text-decoration:none;">
            ${escapeHtml(job.name)}
          </a>
        </td>
        <td><code>${escapeHtml(job.engine)}</code></td>
        <td><code>${escapeHtml(job.schedule)}</code></td>
        <td><span class="badge ${statusClass}">${escapeHtml(job.last_status || 'pending')}</span></td>
        <td>${lastRun}</td>
        <td>${nextRun}</td>
        <td>
          <button class="btn btn-sm" onclick="runJob('${job.name}')" ${job.is_running ? 'disabled' : ''}>
            ${job.is_running ? 'Running...' : 'Run Now'}
          </button>
        </td>
      `;
      tbody.appendChild(tr);
    });

    updateJobSelector(jobs);
  } catch (err) {
    console.error('Failed to load jobs:', err);
  }
}

async function runJob(name) {
  try {
    const res = await fetch(`/api/jobs/${encodeURIComponent(name)}/run`, {
      method: 'POST',
      headers: {
        'X-CSRF-Token': csrfToken,
        'Content-Type': 'application/json'
      }
    });

    if (res.status === 409) {
      alert(`Job "${name}" is already running.`);
      return;
    }

    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      alert(`Error starting job: ${data.error || res.statusText}`);
      return;
    }

    // Refresh immediately to show running state
    loadJobs();
    if (currentJobForDetails === name) {
      setTimeout(() => loadJobLogs(name), 500);
    }
  } catch (err) {
    alert(`Failed to trigger job: ${err}`);
  }
}

function openJobDetails(name) {
  currentJobForDetails = name;
  const select = document.getElementById('details-job-select');
  if (select) select.value = name;
  showTab('tab-details');
}

function updateJobSelector(jobs) {
  const select = document.getElementById('details-job-select');
  if (!select) return;

  const prevValue = select.value;
  select.innerHTML = '';
  jobs.forEach(j => {
    const opt = document.createElement('option');
    opt.value = j.name;
    opt.textContent = `${j.name} (${j.engine})`;
    select.appendChild(opt);
  });

  if (prevValue && jobs.some(j => j.name === prevValue)) {
    select.value = prevValue;
  } else if (jobs.length > 0 && !currentJobForDetails) {
    currentJobForDetails = jobs[0].name;
    select.value = jobs[0].name;
  }
}

async function selectJobDetails(name) {
  if (!name) return;
  currentJobForDetails = name;
  document.getElementById('details-job-title').textContent = `Job: ${name}`;
  await Promise.all([loadJobBackups(name), loadJobLogs(name)]);
}

async function loadJobBackups(name) {
  const tbody = document.getElementById('backups-tbody');
  if (!tbody) return;
  tbody.innerHTML = '<tr><td colspan="4" style="text-align:center;">Loading backups...</td></tr>';

  try {
    const res = await fetch(`/api/jobs/${encodeURIComponent(name)}/backups`);
    if (!res.ok) throw new Error(res.statusText);
    const files = await res.json();

    tbody.innerHTML = '';
    if (!files || files.length === 0) {
      tbody.innerHTML = '<tr><td colspan="4" style="text-align:center; color: var(--text-muted);">No backups found for this job.</td></tr>';
      return;
    }

    files.forEach(f => {
      const tr = document.createElement('tr');
      const date = f.mod_time ? new Date(f.mod_time).toLocaleString() : 'N/A';
      const size = formatBytes(f.size);
      const downloadUrl = `/api/jobs/${encodeURIComponent(name)}/backups/${encodeURIComponent(f.filename)}/download`;

      tr.innerHTML = `
        <td><code>${escapeHtml(f.filename)}</code></td>
        <td>${size}</td>
        <td>${date}</td>
        <td>
          <a href="${downloadUrl}" class="btn btn-sm btn-secondary" download>Download</a>
        </td>
      `;
      tbody.appendChild(tr);
    });
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="4" style="color:var(--danger);">Failed to load backups: ${escapeHtml(err.message)}</td></tr>`;
  }
}

async function loadJobLogs(name) {
  const viewer = document.getElementById('log-viewer');
  if (!viewer) return;

  try {
    const res = await fetch(`/api/jobs/${encodeURIComponent(name)}/logs`);
    if (!res.ok) throw new Error(res.statusText);
    const data = await res.json();

    if (!data.logs || data.logs.length === 0) {
      viewer.textContent = 'No logs available yet for this job.';
      return;
    }

    viewer.textContent = data.logs.join('\n');
    viewer.scrollTop = viewer.scrollHeight;
  } catch (err) {
    viewer.textContent = `Error loading logs: ${err.message}`;
  }
}

async function loadConfig() {
  const editor = document.getElementById('config-editor');
  const pathEl = document.getElementById('config-path');
  const alertEl = document.getElementById('config-alert');
  const saveBtn = document.getElementById('config-save-btn');

  if (!editor) return;
  alertEl.style.display = 'none';

  try {
    const res = await fetch('/api/config');
    if (!res.ok) throw new Error(res.statusText);
    const data = await res.json();

    pathEl.textContent = data.config_path || '';
    editor.value = data.yaml || '';

    if (data.writable) {
      editor.removeAttribute('readonly');
      saveBtn.style.display = 'inline-flex';
    } else {
      editor.setAttribute('readonly', 'true');
      saveBtn.style.display = 'none';
    }
  } catch (err) {
    editor.value = `Failed to load configuration: ${err.message}`;
  }
}

async function saveConfig() {
  const editor = document.getElementById('config-editor');
  const alertEl = document.getElementById('config-alert');
  if (!editor || !alertEl) return;

  alertEl.style.display = 'none';

  try {
    const res = await fetch('/api/config', {
      method: 'POST',
      headers: {
        'X-CSRF-Token': csrfToken,
        'Content-Type': 'application/x-yaml'
      },
      body: editor.value
    });

    const data = await res.json().catch(() => ({}));

    if (!res.ok) {
      alertEl.className = 'alert alert-danger';
      alertEl.textContent = `Save failed: ${data.error || res.statusText}`;
      alertEl.style.display = 'block';
      return;
    }

    alertEl.className = 'alert alert-success';
    alertEl.textContent = 'Configuration saved and scheduler reloaded successfully!';
    alertEl.style.display = 'block';

    // Refresh jobs overview
    setTimeout(loadJobs, 1000);
  } catch (err) {
    alertEl.className = 'alert alert-danger';
    alertEl.textContent = `Save error: ${err.message}`;
    alertEl.style.display = 'block';
  }
}

function formatBytes(bytes) {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
}

function escapeHtml(str) {
  if (!str) return '';
  return str.replace(/[&<>'"]/g, tag => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    "'": '&#39;',
    '"': '&quot;'
  }[tag] || tag));
}

// Auto-poll jobs status every 5 seconds
setInterval(() => {
  const jobsTab = document.getElementById('tab-jobs');
  if (jobsTab && jobsTab.style.display !== 'none') {
    loadJobs();
  }
  const detailsTab = document.getElementById('tab-details');
  if (detailsTab && detailsTab.style.display !== 'none' && currentJobForDetails) {
    loadJobLogs(currentJobForDetails);
  }
}, 5000);

document.addEventListener('DOMContentLoaded', () => {
  loadJobs();
});
