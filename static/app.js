// Theme management
function loadTheme() {
    const theme = localStorage.getItem('theme') || 'default';
    const themeLink = document.getElementById('theme-css');
    const themeSelector = document.getElementById('theme-selector');
    
    console.log('Loading theme:', theme);
    
    const themePath = `/static/themes/${theme}.css`;
    console.log('Setting theme CSS:', themePath);
    themeLink.href = themePath;
    
    if (themeSelector) {
        themeSelector.value = theme;
    }
}

// Layout management
function loadLayout() {
    const layout = localStorage.getItem('layout') || 'sidebar';
    const layoutSelector = document.getElementById('layout-selector');
    const dashboardLayout = document.querySelector('.dashboard-layout');
    
    if (layoutSelector) {
        layoutSelector.value = layout;
    }
    
    if (dashboardLayout) {
        dashboardLayout.className = 'dashboard-layout layout-' + layout;
    }
}

// Update service card status
function updateServiceCard(serviceId, status) {
    const card = document.querySelector(`[data-service-id="${serviceId}"]`);
    if (card) {
        // Remove old status classes
        card.className = card.className.replace(/status-\w+/g, '').trim();
        // Add new status class
        card.classList.add('card', `status-${status}`);
    }
}

// Update metrics display
function updateMetricsDisplay(metrics) {
    document.getElementById('cpu-value').textContent = metrics.CPULoad.toFixed(1) + '%';
    document.getElementById('memory-value').textContent = metrics.MemoryUsed.toFixed(1) + 'GB / ' + metrics.MemoryTotal.toFixed(1) + 'GB';
    document.getElementById('disk-value').textContent = metrics.DiskUsed.toFixed(1) + 'GB / ' + metrics.DiskTotal.toFixed(1) + 'GB';
}

// Search functionality
let searchData = { services: [], bookmarks: [] };

function loadSearchData() {
    // Services
    const serviceCards = document.querySelectorAll('#services .card');
    searchData.services = Array.from(serviceCards).map(card => {
        const iconEl = card.querySelector('img, .iconify');
        let iconHTML = '';
        if (iconEl) {
            const cloned = iconEl.cloneNode(true);
            if (cloned.tagName === 'IMG') {
                cloned.style.width = '24px';
                cloned.style.height = '24px';
                cloned.style.objectFit = 'contain';
            } else {
                cloned.setAttribute('data-width', '24');
                cloned.setAttribute('data-height', '24');
            }
            iconHTML = cloned.outerHTML;
        }
        return {
            name: card.querySelector('h3')?.textContent || '',
            description: card.querySelector('p')?.textContent || '',
            url: card.getAttribute('href') || '',
            iconHTML: iconHTML,
            type: 'service'
        };
    });

    // Bookmarks (uniquement ceux du layout actif)
    let layout = localStorage.getItem('layout') || 'sidebar';
    let bookmarkContainer = layout === 'sidebar'
        ? document.getElementById('bookmarks')
        : document.getElementById('bookmarks-bottom');
    const bookmarkCards = bookmarkContainer ? bookmarkContainer.querySelectorAll('.bookmark-card') : [];
    searchData.bookmarks = Array.from(bookmarkCards).map(card => ({
        name: card.querySelector('.bookmark-name')?.textContent || '',
        abbr: card.querySelector('h3')?.textContent || '',
        url: card.getAttribute('href') || '',
        iconHTML: '<span class="iconify" data-icon="mdi:bookmark-outline" data-width="24" data-height="24"></span>',
        type: 'bookmark'
    }));
}


function openSearchPopup() {
    document.getElementById('search-popup').classList.add('active');
    document.getElementById('search-input').focus();
}

function closeSearchPopup() {
    document.getElementById('search-popup').classList.remove('active');
    document.getElementById('search-input').value = '';
    document.getElementById('search-results').innerHTML = '';
}

function performSearch(query) {
    if (!query || query.length < 1) {
        document.getElementById('search-results').innerHTML = '';
        return;
    }
    
    const lowerQuery = query.toLowerCase();
    const allItems = [...searchData.services, ...searchData.bookmarks];
    
    const results = allItems.filter(item => {
        return item.name.toLowerCase().includes(lowerQuery) ||
               (item.description && item.description.toLowerCase().includes(lowerQuery)) ||
               (item.abbr && item.abbr.toLowerCase().includes(lowerQuery));
    }).slice(0, 6);
    
    const resultsHTML = results.map((item, index) => {
        let iconHTML = item.iconHTML;
        
        return `
            <div class="search-result-item ${index === 0 ? 'selected' : ''}" data-index="${index}" data-url="${item.url}">
                <span class="result-type">${iconHTML}</span>
                <div class="result-content">
                    <div class="result-name">${item.name}</div>
                    ${item.description ? `<div class="result-desc">${item.description}</div>` : ''}
                </div>
            </div>
        `;
    }).join('');
    
    document.getElementById('search-results').innerHTML = resultsHTML || '<div class="no-results">No results found</div>';
    
    // Add click handlers
    document.querySelectorAll('.search-result-item').forEach(item => {
        item.addEventListener('click', function() {
            window.open(this.dataset.url, '_blank');
            closeSearchPopup();
        });
    });
}

document.addEventListener('DOMContentLoaded', function() {
    // Lock body height to prevent viewport changes from affecting background
    const lockBodyHeight = () => {
        document.body.style.height = `${window.innerHeight}px`;
    };
    
    // Set initial height on load
    lockBodyHeight();
    window.addEventListener('load', lockBodyHeight);
    
    // Only update on orientation changes, not on scroll
    window.addEventListener('orientationchange', () => setTimeout(lockBodyHeight, 100));
    
    // Load saved theme and layout
    loadTheme();
    loadLayout();
    
    // Theme selector change
    const themeSelector = document.getElementById('theme-selector');
    if (themeSelector) {
        themeSelector.addEventListener('change', function(e) {
            localStorage.setItem('theme', e.target.value);
            loadTheme();
        });
    }
    
    // Layout selector change
    const layoutSelector = document.getElementById('layout-selector');
    if (layoutSelector) {
        layoutSelector.addEventListener('change', function(e) {
            localStorage.setItem('layout', e.target.value);
            loadLayout();
        });
    }
    
    // No more periodic updates - everything comes via SSE
    
    // Load search data from rendered page
    loadSearchData();
    
    // Keyboard shortcuts
    document.addEventListener('keydown', function(e) {
        // Open search on Enter or any letter/number key (not in input fields)
        if (e.target.tagName !== 'INPUT' && e.target.tagName !== 'SELECT') {
            if (e.key === 'Enter') {
                e.preventDefault();
                openSearchPopup();
                return;
            }
            
            if (!e.ctrlKey && !e.metaKey && !e.altKey && 
                e.key.length === 1 && e.key.match(/[a-zA-Z0-9]/)) {
                e.preventDefault();
                openSearchPopup();
                document.getElementById('search-input').value = e.key;
                performSearch(e.key);
            }
        }
        
        // Close on Escape
        if (e.key === 'Escape') {
            closeSearchPopup();
        }
        
        // Navigate with arrows and Enter
        if (document.getElementById('search-popup').classList.contains('active')) {
            const results = document.querySelectorAll('.search-result-item');
            const selected = document.querySelector('.search-result-item.selected');
            let currentIndex = selected ? parseInt(selected.dataset.index) : -1;
            
            if (e.key === 'ArrowDown' || e.key === 'Tab') {
                e.preventDefault();
                currentIndex = Math.min(currentIndex + 1, results.length - 1);
            } else if (e.key === 'ArrowUp' || (e.key === 'Tab' && e.shiftKey)) {
                e.preventDefault();
                currentIndex = Math.max(currentIndex - 1, 0);
            } else if (e.key === 'Enter' && selected) {
                e.preventDefault();
                window.open(selected.dataset.url, '_blank');
                closeSearchPopup();
                return;
            }
            
            results.forEach((item, index) => {
                item.classList.toggle('selected', index === currentIndex);
            });
        }
    });
    
    // Search input
    document.getElementById('search-input').addEventListener('input', function(e) {
        performSearch(e.target.value);
    });
    
    // Close popup on background click
    document.getElementById('search-popup').addEventListener('click', function(e) {
        if (e.target.id === 'search-popup') {
            closeSearchPopup();
        }
    });
    
    // SSE connection management
    let evtSource = null;
    
    function connectSSE() {
        // Close existing connection if any
        if (evtSource) {
            evtSource.close();
        }
        
        evtSource = new EventSource('/events');
        
        evtSource.onmessage = function(event) {
            try {
                const data = JSON.parse(event.data);

                if (data.type === 'reload') {
                    window.location.reload();
                } else if (data.type === 'metrics') {
                    updateMetricsDisplay(data.data);
                } else if (data.type === 'service') {
                    updateServiceCard(data.data.id, data.data.status);
                }
            } catch (err) {
                console.error('Error parsing SSE message:', err);
            }
        };
        
        evtSource.onerror = function(err) {
            console.error('SSE error:', err);
        };
    }
    
    // Initial connection
    connectSSE();
    
    // Reconnect when page becomes visible
    document.addEventListener('visibilitychange', function() {
        if (!document.hidden && (!evtSource || evtSource.readyState === EventSource.CLOSED)) {
            console.log('Page visible, reconnecting SSE');
            connectSSE();
        }
    });
});
