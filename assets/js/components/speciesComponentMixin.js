// Alpine.js mixin for species components
// This provides reusable functions for species lists and inputs

window.speciesComponentMixin = {
    /**
     * Mixin for species list components
     * Provides functions for updating predictions, adding species, and managing lists
     */
    speciesListMixin: function(listType) {
        return {
            // Data properties
            newSpecies: '',
            predictions: [],
            editIndex: null,
            editValue: '',
            hasChanges: false,

            /**
             * Updates filtered predictions based on input
             * @param {string} input - The input text to filter by
             * @param {string} listType - The type of list (e.g., 'Include', 'Exclude')
             */
            updatePredictions(input, listType) {
                if (!input) {
                    this.predictions = [];
                    return;
                }
                
                // Get the appropriate source list based on list type
                const sourceList = this.getSourceList(listType);
                if (!sourceList || sourceList.length === 0) {
                    this.predictions = [];
                    return;
                }
                
                // Filter out species that are already in the list (case insensitive)
                const existingSpeciesLower = this.speciesSettings[listType].map(s => s.toLowerCase());
                const inputLower = input.toLowerCase();
                
                this.predictions = sourceList
                    .filter(species => {
                        const speciesLower = species.toLowerCase();
                        return speciesLower.includes(inputLower) && !existingSpeciesLower.includes(speciesLower);
                    })
                    .slice(0, 5); // limit to 5 suggestions
            },
            
            /**
             * Add a species to the specified list
             * @param {string} list - The list to add to (e.g., 'Include', 'Exclude')
             */
            addSpecies(list) {
                const newSpecies = this['new' + list + 'Species'].trim();
                if (!newSpecies) return;
                
                // Check if species already exists (case insensitive)
                const newSpeciesLower = newSpecies.toLowerCase();
                const exists = this.speciesSettings[list].some(s => s.toLowerCase() === newSpeciesLower);
                
                if (!exists) {
                    this.speciesSettings[list].push(newSpecies);
                    this.hasChanges = true;
                }
                
                // Always clear input and predictions
                this['new' + list + 'Species'] = '';
                this.predictions = [];
            },
            
            /**
             * Remove a species from the specified list
             * @param {string} list - The list to remove from
             * @param {string|number} item - The species to remove or its index
             */
            removeSpecies(list, item) {
                if (typeof item === 'number') {
                    // Remove by index
                    this.speciesSettings[list].splice(item, 1);
                } else {
                    // Remove by value
                    this.speciesSettings[list] = this.speciesSettings[list].filter(s => s !== item);
                }
                this.hasChanges = true;
            },
            
            /**
             * Start editing a species
             * @param {number} index - The index of the species to edit
             */
            startEdit(index) {
                this.editIndex = index;
                this.editValue = this.speciesSettings[listType][index];
            },
            
            /**
             * Save edited species
             */
            saveEdit() {
                if (this.editIndex !== null && this.editValue.trim()) {
                    this.speciesSettings[listType][this.editIndex] = this.editValue.trim();
                    this.hasChanges = true;
                    this.cancelEdit();
                }
            },
            
            /**
             * Cancel editing
             */
            cancelEdit() {
                this.editIndex = null;
                this.editValue = '';
            },
            
            /**
             * Helper method to determine the appropriate source list
             * Override this in your component as needed
             */
            getSourceList(listType) {
                return listType === 'Include' ? this.allSpecies : this.filteredSpecies;
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