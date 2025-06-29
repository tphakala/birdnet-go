// Notification Bell Alpine.js Component
document.addEventListener('alpine:init', () => {
    Alpine.data('notificationBell', () => ({
        // State
        notifications: [],
        unreadCount: 0,
        dropdownOpen: false,
        loading: false,
        hasUnread: false,
        sseConnection: null,
        reconnectTimeout: null,
        reconnectDelay: 1000,
        maxReconnectDelay: 30000,
        soundEnabled: false,
        debugMode: false,
        
        // Initialize
        init() {
            // Get debug mode from data attribute
            const debugAttr = this.$el.getAttribute('data-debug-mode');
            this.debugMode = debugAttr === 'true';
            
            // Load notifications
            this.loadNotifications();
            
            // Connect to SSE
            this.connectSSE();
            
            // Load sound preference
            this.soundEnabled = localStorage.getItem('notificationSound') === 'true';
            
            // Cleanup on page unload
            window.addEventListener('beforeunload', () => {
                this.cleanup();
            });
        },
        
        // Load notifications from API
        async loadNotifications() {
            this.loading = true;
            try {
                const response = await fetch('/api/v2/notifications?limit=20&status=unread');
                if (response.ok) {
                    const data = await response.json();
                    // Filter notifications based on debug mode
                    this.notifications = (data.notifications || []).filter(n => this.shouldShowNotification(n));
                    this.updateUnreadCount();
                }
            } catch (error) {
                console.error('Failed to load notifications:', error);
            } finally {
                this.loading = false;
            }
        },
        
        // Connect to SSE for real-time notifications
        connectSSE() {
            if (this.sseConnection) {
                this.sseConnection.close();
            }
            
            this.sseConnection = new EventSource('/api/v2/notifications/stream');
            
            this.sseConnection.onopen = () => {
                console.log('Notification SSE connected');
                this.reconnectDelay = 1000; // Reset delay on successful connection
            };
            
            this.sseConnection.onmessage = (event) => {
                try {
                    const data = JSON.parse(event.data);
                    this.handleSSEMessage(data);
                } catch (error) {
                    console.error('Failed to parse SSE message:', error);
                }
            };
            
            this.sseConnection.onerror = (error) => {
                console.error('SSE connection error:', error);
                this.sseConnection.close();
                this.scheduleReconnect();
            };
        },
        
        // Handle SSE messages
        handleSSEMessage(data) {
            switch (data.eventType) {
                case 'connected':
                    console.log('Connected to notification stream:', data.clientId);
                    break;
                    
                case 'notification':
                    this.addNotification(data);
                    break;
                    
                case 'heartbeat':
                    // Heartbeat received, connection is alive
                    break;
                    
                default:
                    console.log('Unknown SSE event type:', data.eventType);
            }
        },
        
        // Check if notification should be shown based on debug mode
        shouldShowNotification(notification) {
            // Always show user-facing notifications
            if (notification.type === 'detection' || 
                notification.priority === 'critical' ||
                notification.priority === 'high') {
                return true;
            }
            
            // In debug mode, show all notifications
            if (this.debugMode) {
                return true;
            }
            
            // Filter out system/error notifications when not in debug mode
            if (notification.type === 'error' || 
                notification.type === 'system' || 
                notification.type === 'warning') {
                return false;
            }
            
            return true;
        },
        
        // Add new notification
        addNotification(notification) {
            // Check if notification should be shown
            if (!this.shouldShowNotification(notification)) {
                return;
            }
            
            // Add to beginning of array
            this.notifications.unshift(notification);
            
            // Limit to 20 most recent
            if (this.notifications.length > 20) {
                this.notifications.pop();
            }
            
            this.updateUnreadCount();
            
            // Wiggle animation
            this.hasUnread = true;
            setTimeout(() => {
                this.hasUnread = false;
            }, 1000);
            
            // Play sound if enabled and notification is high priority
            if (this.soundEnabled && 
                (notification.priority === 'critical' || notification.priority === 'high')) {
                this.playNotificationSound();
            }
            
            // Show browser notification if permitted
            if (notification.priority === 'critical') {
                this.showBrowserNotification(notification);
            }
        },
        
        // Update unread count
        updateUnreadCount() {
            this.unreadCount = this.notifications.filter(n => !n.read).length;
        },
        
        // Toggle dropdown
        toggleDropdown() {
            this.dropdownOpen = !this.dropdownOpen;
            if (this.dropdownOpen && this.unreadCount > 0) {
                // Refresh notifications when opening
                this.loadNotifications();
            }
        },
        
        // Handle notification click
        async handleNotificationClick(notification) {
            if (!notification.read) {
                await this.markAsRead(notification.id);
            }
            
            // Close dropdown
            this.dropdownOpen = false;
            
            // Navigate based on notification type
            if (notification.metadata && notification.metadata.link) {
                window.location.href = notification.metadata.link;
            }
        },
        
        // Mark notification as read
        async markAsRead(notificationId) {
            try {
                const response = await fetch(`/api/v2/notifications/${notificationId}/read`, {
                    method: 'PUT',
                    headers: {
                        'X-CSRF-Token': document.querySelector('meta[name="csrf-token"]').content
                    }
                });
                
                if (response.ok) {
                    const notification = this.notifications.find(n => n.id === notificationId);
                    if (notification) {
                        notification.read = true;
                        this.updateUnreadCount();
                    }
                }
            } catch (error) {
                console.error('Failed to mark notification as read:', error);
            }
        },
        
        // Mark all as read
        async markAllAsRead() {
            const unreadIds = this.notifications
                .filter(n => !n.read)
                .map(n => n.id);
                
            for (const id of unreadIds) {
                await this.markAsRead(id);
            }
        },
        
        // Play notification sound
        playNotificationSound() {
            const audio = new Audio('/assets/sounds/notification.mp3');
            audio.volume = 0.5;
            audio.play().catch(e => console.log('Could not play notification sound:', e));
        },
        
        // Show browser notification
        showBrowserNotification(notification) {
            if ('Notification' in window && Notification.permission === 'granted') {
                new Notification(notification.title, {
                    body: notification.message,
                    icon: '/assets/images/favicon-32x32.png',
                    tag: notification.id
                });
            }
        },
        
        // Get notification icon class
        getNotificationIconClass(notification) {
            const baseClass = 'w-8 h-8 rounded-full flex items-center justify-center';
            
            switch (notification.type) {
                case 'error':
                    return baseClass + ' bg-error/20 text-error';
                case 'warning':
                    return baseClass + ' bg-warning/20 text-warning';
                case 'info':
                    return baseClass + ' bg-info/20 text-info';
                case 'detection':
                    return baseClass + ' bg-success/20 text-success';
                case 'system':
                    return baseClass + ' bg-primary/20 text-primary';
                default:
                    return baseClass + ' bg-base-300 text-base-content';
            }
        },
        
        // Get priority badge class
        getPriorityBadgeClass(priority) {
            switch (priority) {
                case 'critical':
                    return 'badge-error';
                case 'high':
                    return 'badge-warning';
                case 'medium':
                    return 'badge-info';
                case 'low':
                    return 'badge-ghost';
                default:
                    return 'badge-ghost';
            }
        },
        
        // Format time ago
        formatTimeAgo(timestamp) {
            const date = new Date(timestamp);
            const now = new Date();
            const seconds = Math.floor((now - date) / 1000);
            
            if (seconds < 60) return 'just now';
            if (seconds < 3600) return Math.floor(seconds / 60) + 'm ago';
            if (seconds < 86400) return Math.floor(seconds / 3600) + 'h ago';
            if (seconds < 604800) return Math.floor(seconds / 86400) + 'd ago';
            
            return date.toLocaleDateString();
        },
        
        // Schedule reconnection
        scheduleReconnect() {
            if (this.reconnectTimeout) {
                clearTimeout(this.reconnectTimeout);
            }
            
            this.reconnectTimeout = setTimeout(() => {
                console.log('Attempting to reconnect to notification stream...');
                this.connectSSE();
                
                // Exponential backoff
                this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxReconnectDelay);
            }, this.reconnectDelay);
        },
        
        // Cleanup
        cleanup() {
            if (this.sseConnection) {
                this.sseConnection.close();
            }
            if (this.reconnectTimeout) {
                clearTimeout(this.reconnectTimeout);
            }
        }
    }));
});

// Request notification permission on page load
document.addEventListener('DOMContentLoaded', () => {
    if ('Notification' in window && Notification.permission === 'default') {
        // Don't request immediately, wait for user interaction
        document.addEventListener('click', function requestPermission() {
            Notification.requestPermission();
            document.removeEventListener('click', requestPermission);
        }, { once: true });
    }
});