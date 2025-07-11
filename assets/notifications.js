// Notification Bell Alpine.js Component
document.addEventListener('alpine:init', () => {
    Alpine.data('notificationBell', () => ({
        // State
        notifications: [],
        unreadCount: 0,
        dropdownOpen: false,
        loading: true, // Start with loading true to prevent flash
        hasUnread: false,
        sseConnection: null,
        reconnectTimeout: null,
        reconnectDelay: 1000,
        maxReconnectDelay: 30000,
        soundEnabled: false,
        debugMode: false,
        animationTimeout: null,
        
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
            
            // Create bound handler for cleanup
            this.notificationDeletedHandler = (event) => {
                this.handleNotificationDeleted(event.detail);
            };
            
            // Listen for notification deletion events
            window.addEventListener('notification-deleted', this.notificationDeletedHandler);
            
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
                
                // Only reconnect if not explicitly closed
                if (this.sseConnection.readyState !== EventSource.CLOSED) {
                    this.sseConnection.close();
                }
                
                // Don't reconnect if page is being unloaded or offline
                if (!window.navigator.onLine || document.hidden) {
                    return;
                }
                
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
            // Clear any existing animation timeout
            if (this.animationTimeout) {
                clearTimeout(this.animationTimeout);
            }
            
            this.hasUnread = true;
            this.animationTimeout = setTimeout(() => {
                this.hasUnread = false;
                this.animationTimeout = null;
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
        
        // Handle notification deleted event from other components
        handleNotificationDeleted(detail) {
            // Remove the notification from our local array
            const index = this.notifications.findIndex(n => n.id === detail.id);
            if (index !== -1) {
                this.notifications.splice(index, 1);
                
                // Update unread count if the deleted notification was unread
                if (detail.wasUnread) {
                    this.updateUnreadCount();
                }
                
                if (this.debugMode) {
                    console.log('Notification bell updated after deletion:', {
                        id: detail.id,
                        wasUnread: detail.wasUnread,
                        newUnreadCount: this.unreadCount
                    });
                }
            }
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
            // Mark as read if unread (fire and forget, don't wait)
            if (!notification.read) {
                this.markAsRead(notification.id);
            }
            
            // Navigate to notifications page
            window.location.href = '/notifications';
        },
        
        // Helper function to safely get CSRF token
        getCSRFToken() {
            return NotificationUtils.getCSRFToken();
        },
        
        // Mark notification as read
        async markAsRead(notificationId) {
            try {
                const response = await fetch(`/api/v2/notifications/${notificationId}/read`, {
                    method: 'PUT',
                    headers: {
                        'X-CSRF-Token': this.getCSRFToken()
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
                
            // Parallel execution for better performance
            await Promise.all(unreadIds.map(id => this.markAsRead(id)));
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
            return NotificationUtils.getNotificationIconClass(notification);
        },
        
        // Get priority badge class
        getPriorityBadgeClass(priority) {
            return NotificationUtils.getPriorityBadgeClass(priority);
        },
        
        // Format time ago
        formatTimeAgo(timestamp) {
            return NotificationUtils.formatTimeAgo(timestamp);
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
            if (this.animationTimeout) {
                clearTimeout(this.animationTimeout);
            }
            // Remove event listener
            if (this.notificationDeletedHandler) {
                window.removeEventListener('notification-deleted', this.notificationDeletedHandler);
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