// Alpine.js mixin for species components
// This provides reusable functions for species lists and inputs

window.speciesComponentMixin = {
    /**
     * Mixin for species list components
     * Provides functions for updating predictions, adding species, and managing lists
     * @param {string} listType - The type of list to handle (e.g., 'Include', 'Exclude', 'Species')
     * @returns {Object} - Alpine.js mixin with methods for handling species lists
     */
    speciesListMixin: function(listType) {
        return {
            // Data properties
            newSpecies: '',
            predictions: [],
            editIndex: null,
            editSpecies: '',
            showEditSpecies: false,
            hasChanges: false,

            /**
             * Gets the current species list based on the component structure
             * Components can override this to provide their specific list structure
             * @returns {Array} - The current species list
             */
            getSpeciesList() {
                // Default implementation for species settings structure
                if (this.speciesSettings && this.speciesSettings[listType]) {
                    return this.speciesSettings[listType];
                }
                // Dog bark filter structure
                else if (this.dogBarkFilter && this.dogBarkFilter.species) {
                    return this.dogBarkFilter.species;
                }
                // Fallback empty array
                return [];
            },
            
            /**
             * Updates the species list with a new value
             * Components can override this to implement specific update logic
             * @param {Array} newList - The updated species list
             */
            setSpeciesList(newList) {
                // Default implementation for species settings structure
                if (this.speciesSettings && this.speciesSettings[listType]) {
                    this.speciesSettings[listType] = newList;
                }
                // Dog bark filter structure
                else if (this.dogBarkFilter) {
                    this.dogBarkFilter.species = newList;
                }
                
                // Mark changes
                this.hasChanges = true;
                
                // Allow components to implement additional logic after modifications
                if (typeof this.afterModification === 'function') {
                    this.afterModification();
                }
            },
            
            /**
             * Hook called after modifications to the species list
             * Components can override this to implement additional logic
             */
            afterModification() {
                // Placeholder for component-specific logic
                // By default, just ensure hasChanges is set
                this.hasChanges = true;
            },

            /**
             * Updates filtered predictions based on input
             * @param {string} input - The input text to filter by
             * @param {string} [specificListType] - Optional override for the list type
             */
            updatePredictions(input, specificListType) {
                if (!input) {
                    this.predictions = [];
                    return;
                }
                
                const targetListType = specificListType || listType;
                
                // Get the appropriate source list based on list type
                const sourceList = this.getSourceList(targetListType);
                if (!sourceList || sourceList.length === 0) {
                    this.predictions = [];
                    return;
                }
                
                // Get the current species list
                const existingSpecies = this.getSpeciesList();
                
                // Filter out species that are already in the list (case insensitive)
                const existingSpeciesLower = existingSpecies.map(s => s.toLowerCase());
                const inputLower = input.toLowerCase();
                
                this.predictions = sourceList
                    .filter(species => {
                        const speciesLower = species.toLowerCase();
                        return speciesLower.includes(inputLower) && !existingSpeciesLower.includes(speciesLower);
                    })
                    .slice(0, 5); // limit to 5 suggestions
            },
            
            /**
             * Add a species to the list
             * @param {string} [specificList] - Optional override for the list type
             */
            addSpecies(specificList) {
                const targetList = specificList || listType;
                
                // Get input value based on context
                let newSpeciesValue;
                if (this['new' + targetList + 'Species'] !== undefined) {
                    newSpeciesValue = this['new' + targetList + 'Species'].trim();
                } else {
                    newSpeciesValue = this.newSpecies.trim();
                }
                
                if (!newSpeciesValue) {
                    return; // Don't add empty strings
                }
                
                // Get the current species list
                const currentList = this.getSpeciesList();
                
                // Check if species already exists (case insensitive)
                const newSpeciesLower = newSpeciesValue.toLowerCase();
                const exists = currentList.some(s => s.toLowerCase() === newSpeciesLower);
                
                if (!exists) {
                    // Create a new list with the added species
                    const updatedList = [...currentList, newSpeciesValue];
                    
                    // Update the list
                    this.setSpeciesList(updatedList);
                }
                
                // Clear input based on context
                if (this['new' + targetList + 'Species'] !== undefined) {
                    this['new' + targetList + 'Species'] = '';
                } else {
                    this.newSpecies = '';
                }
                
                // Always clear predictions
                this.predictions = [];
            },
            
            /**
             * Remove a species from the list
             * @param {Event|number} event - Event with detail.index or direct index number
             * @param {string} [specificList] - Optional override for the list type
             * @param {string} [item] - Optional specific item to remove (by value not index)
             */
            removeSpecies(event, specificList, item) {
                // Handle removal by direct value
                if (item !== undefined) {
                    const currentList = this.getSpeciesList();
                    const updatedList = currentList.filter(s => s !== item);
                    this.setSpeciesList(updatedList);
                    return;
                }
                
                // Extract index from event or use directly if it's a number
                let index;
                let eventListType;
                
                if (typeof event === 'object') {
                    // If it's an event from the window event dispatch
                    if (event.detail) {
                        index = event.detail.index;
                        eventListType = event.detail.listType;
                    }
                } else {
                    // If it's a direct index
                    index = event;
                }
                
                // Check if the list type matches (if a list type was provided)
                const targetListType = specificList || listType;
                if (eventListType && eventListType !== targetListType) {
                    return; // Skip if list types don't match
                }
                
                if (index !== undefined) {
                    const currentList = this.getSpeciesList();
                    // Create a new list without the item at the specified index
                    const updatedList = [...currentList];
                    updatedList.splice(index, 1);
                    this.setSpeciesList(updatedList);
                }
            },
            
            /**
             * Start editing a species
             * @param {Event|number} event - Event with detail.index or direct index number
             */
            startEdit(event) {
                // Extract index from event or use directly if it's a number
                let index;
                let eventListType;
                
                if (typeof event === 'object') {
                    // If it's an event from the window event dispatch
                    if (event.detail) {
                        index = event.detail.index;
                        eventListType = event.detail.listType;
                    }
                } else {
                    // If it's a direct index
                    index = event;
                }
                
                // Check if the list type matches (if a list type was provided)
                if (eventListType && eventListType !== listType) {
                    return; // Skip if list types don't match
                }
                
                if (index !== undefined) {
                    const currentList = this.getSpeciesList();
                    
                    if (currentList[index]) {
                        this.editIndex = index;
                        this.editSpecies = currentList[index];
                        this.showEditSpecies = true;
                    }
                }
            },
            
            /**
             * Save edited species
             * @param {Event|undefined} event - Optional event object from dispatch
             */
            saveEdit(event) {
                // Handle event from dispatch if provided
                if (event && event.detail) {
                    const eventListType = event.detail.listType;
                    // Check if the list type matches
                    if (eventListType && eventListType !== listType) {
                        return; // Skip if list types don't match
                    }
                }
                
                if (this.editIndex !== null && this.editSpecies && this.editSpecies.trim()) {
                    const currentList = this.getSpeciesList();
                    const newValue = this.editSpecies.trim();
                    const oldValue = currentList[this.editIndex];
                    
                    // Only update if value has changed
                    if (oldValue !== newValue) {
                        const updatedList = [...currentList];
                        updatedList[this.editIndex] = newValue;
                        this.setSpeciesList(updatedList);
                    }
                }
                
                // Reset edit state
                this.editIndex = null;
                this.editSpecies = '';
                this.showEditSpecies = false;
            },
            
            /**
             * Cancel editing
             * @param {Event|undefined} event - Optional event object from dispatch
             */
            cancelEdit(event) {
                // Handle event from dispatch if provided
                if (event && event.detail) {
                    const eventListType = event.detail.listType;
                    // Check if the list type matches
                    if (eventListType && eventListType !== listType) {
                        return; // Skip if list types don't match
                    }
                }
                
                // Properly reset all edit state
                this.editIndex = null;
                this.editSpecies = '';
                this.showEditSpecies = false;
                
                // Also unfocus any active input elements to ensure clean state
                if (document.activeElement && document.activeElement.tagName === 'INPUT') {
                    document.activeElement.blur();
                }
            },
            
            /**
             * Helper method to determine the appropriate source list
             * Override this in your component as needed
             * @param {string} type - The type of list to get source data for
             * @returns {Array} - The source list of species
             */
            getSourceList(type) {
                // Components should override this method to provide the correct source list
                return this.allSpecies || this.filteredSpecies || this.allowedSpecies || [];
            },
            
            /**
             * Reset change tracking
             */
            resetChanges() {
                this.hasChanges = false;
            }
        };
    }
}; 