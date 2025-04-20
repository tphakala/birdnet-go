// Species utility functions for BirdNET-Go
// These functions handle species list operations including:
// - Prediction filtering
// - Adding species to lists
// - Removing species from lists
// - Checking for duplicates

/**
 * Updates the predictions array based on input text
 * @param {string} input - The input text to search for
 * @param {Array} sourceList - The source list of species to search in
 * @param {Array} existingSpecies - The list of already selected species
 * @param {function} setPredictions - Function to set predictions array
 */
function updateSpeciesPredictions(input, sourceList, existingSpecies, setPredictions) {
    // Clear predictions if input is empty or source list is invalid
    if (!input || !sourceList || sourceList.length === 0) {
        setPredictions([]);
        return;
    }
    
    // Create lowercase arrays for case-insensitive comparison
    const existingSpeciesLower = existingSpecies.map(s => s.toLowerCase());
    const inputLower = input.toLowerCase();
    
    // Filter the source list to find matches that aren't already in the existing list
    const predictions = sourceList
        .filter(species => {
            const speciesLower = species.toLowerCase();
            return speciesLower.includes(inputLower) && !existingSpeciesLower.includes(speciesLower);
        })
        .slice(0, 5); // limit to 5 suggestions
    
    setPredictions(predictions);
}

/**
 * Adds a species to a list if it's not already there
 * @param {string} newSpecies - The species to add
 * @param {Array} speciesList - The list to add to
 * @param {function} setSpeciesList - Function to update the species list
 * @param {function} setNewSpecies - Function to clear the input field
 * @param {function} setPredictions - Function to clear predictions
 * @param {function} setHasChanges - Function to mark changes (optional)
 * @returns {boolean} - Whether the species was added
 */
function addSpeciesToList(newSpecies, speciesList, setSpeciesList, setNewSpecies, setPredictions, setHasChanges = null) {
    const trimmedSpecies = newSpecies.trim();
    
    // Don't add empty strings
    if (!trimmedSpecies) {
        return false;
    }
    
    // Check if species already exists (case insensitive)
    const newSpeciesLower = trimmedSpecies.toLowerCase();
    const exists = speciesList.some(s => s.toLowerCase() === newSpeciesLower);
    
    // Add species if it doesn't exist
    if (!exists) {
        const updatedList = [...speciesList, trimmedSpecies];
        setSpeciesList(updatedList);
        
        // Mark changes if needed
        if (setHasChanges) {
            setHasChanges(true);
        }
    }
    
    // Always clear input and predictions
    setNewSpecies('');
    setPredictions([]);
    
    return !exists;
}

/**
 * Removes a species from a list
 * @param {number|Event} indexOrEvent - The index of the species to remove or an event with detail.index
 * @param {Array} speciesList - The list to remove from
 * @param {function} setSpeciesList - Function to update the species list
 * @param {function} setHasChanges - Function to mark changes (optional)
 * @returns {Array} - The updated species list
 */
function removeSpeciesFromList(indexOrEvent, speciesList, setSpeciesList, setHasChanges = null) {
    // Extract index from event or use directly if it's a number
    let index;
    if (typeof indexOrEvent === 'object') {
        // If it's an event from the window event dispatch
        if (indexOrEvent.detail && indexOrEvent.detail.index !== undefined) {
            index = indexOrEvent.detail.index;
        }
    } else {
        // If it's a direct index
        index = indexOrEvent;
    }
    
    if (index === undefined) {
        return speciesList;
    }
    
    // Create a new list without the species at the specified index
    const updatedList = speciesList.filter((_, i) => i !== index);
    setSpeciesList(updatedList);
    
    // Mark changes if needed
    if (setHasChanges) {
        setHasChanges(true);
    }
    
    return updatedList;
}

/**
 * Starts editing a species in a list
 * @param {number|Event} indexOrEvent - The index of the species to edit or an event with detail.index
 * @param {Array} speciesList - The list containing the species to edit
 * @param {function} setEditIndex - Function to set the edit index
 * @param {function} setEditSpecies - Function to set the edit species value
 * @param {function} setShowEditSpecies - Function to show the edit interface
 */
function startEditSpecies(indexOrEvent, speciesList, setEditIndex, setEditSpecies, setShowEditSpecies) {
    // Extract index from event or use directly if it's a number
    let index;
    if (typeof indexOrEvent === 'object') {
        // If it's an event from the window event dispatch
        if (indexOrEvent.detail && indexOrEvent.detail.index !== undefined) {
            index = indexOrEvent.detail.index;
        }
    } else {
        // If it's a direct index
        index = indexOrEvent;
    }
    
    if (index !== undefined && speciesList[index]) {
        setEditIndex(index);
        setEditSpecies(speciesList[index]);
        setShowEditSpecies(true);
    }
}

/**
 * Saves an edited species in a list
 * @param {number} editIndex - The index of the species being edited
 * @param {string} editSpecies - The new value for the species
 * @param {Array} speciesList - The list containing the species to edit
 * @param {function} setSpeciesList - Function to update the species list
 * @param {function} setEditIndex - Function to reset the edit index
 * @param {function} setEditSpecies - Function to reset the edit species value
 * @param {function} setShowEditSpecies - Function to hide the edit interface
 * @param {function} setHasChanges - Function to mark changes (optional)
 */
function saveEditSpecies(editIndex, editSpecies, speciesList, setSpeciesList, setEditIndex, setEditSpecies, setShowEditSpecies, setHasChanges = null) {
    if (editIndex === null || editIndex < 0 || !editSpecies || !editSpecies.trim()) {
        // Invalid edit state
        setEditIndex(null);
        setEditSpecies('');
        setShowEditSpecies(false);
        return;
    }
    
    const trimmedValue = editSpecies.trim();
    const oldValue = speciesList[editIndex];
    
    // Only update if the value has changed
    if (oldValue !== trimmedValue) {
        const updatedList = [...speciesList];
        updatedList[editIndex] = trimmedValue;
        setSpeciesList(updatedList);
        
        // Mark changes if needed
        if (setHasChanges) {
            setHasChanges(true);
        }
    }
    
    // Reset edit state
    setEditIndex(null);
    setEditSpecies('');
    setShowEditSpecies(false);
}

/**
 * Parse species text into scientific and common name components
 * @param {string} speciesText - The species text to parse
 * @returns {Object} Object with scientific name, common name, and display name
 */
function parseSpeciesText(speciesText) {
    // Check if the species text is in the format "Scientific Name (Common Name)"
    const match = speciesText.match(/^(.*?)\s*\((.*?)\)$/);
    
    if (match) {
        const scientificName = match[1].trim();
        const commonName = match[2].trim();
        
        return {
            scientificName,
            commonName,
            displayName: `${commonName} (${scientificName})`
        };
    }
    
    // If no match, treat the whole text as both scientific and common name
    return {
        scientificName: speciesText.trim(),
        commonName: speciesText.trim(),
        displayName: speciesText.trim()
    };
}

/**
 * Format a species action object for display or API
 * @param {Object} action - The action object to format
 * @param {boolean} forApi - Whether to format for API submission
 * @returns {Object} Formatted action object
 */
function formatSpeciesAction(action, forApi = false) {
/**
 * Format a species action object for bidirectional conversion between UI display format and API format
 * 
 * @param {Object} action - The action object to format, can be in either display format (lowercase keys)
 *                         or API format (PascalCase keys)
 * @param {boolean} forApi - Direction of conversion:
 *                         - true: Convert from UI format to API format (lowercase → PascalCase)
 *                         - false: Convert from API format to UI format (PascalCase → lowercase)
 * 
 * @returns {Object} Formatted action object with appropriate property structure:
 * 
 * API format (when forApi=true):
 * {
 *   Type: string,             // Action type (e.g., 'ExecuteCommand')
 *   Command: string,          // Command to execute
 *   Parameters: string[],     // Array of parameter strings
 *   ExecuteDefaults: boolean  // Whether to execute with default parameters
 * }
 * 
 * UI display format (when forApi=false):
 * {
 *   type: string,             // Action type (e.g., 'ExecuteCommand') 
 *   command: string,          // Command to execute
 *   parameters: string,       // Comma-separated parameter string
 *   executeDefaults: boolean  // Whether to execute with default parameters
 * }
 * 
 * Notes:
 * - Parameters conversion: comma-separated string ↔ array of strings
 * - Handles missing properties with appropriate defaults
 * - Preserves boolean flag values correctly
 */
    if (forApi) {
        return {
            Type: action.type || 'ExecuteCommand',
            Command: action.command || '',
            Parameters: Array.isArray(action.parameters) 
                ? action.parameters 
                : (typeof action.parameters === 'string' 
                    ? action.parameters.split(',').map(p => p.trim()).filter(p => p) 
                    : []),
            ExecuteDefaults: action.executeDefaults === true
        };
    } else {
        return {
            type: action.Type || 'ExecuteCommand',
            command: action.Command || '',
            parameters: Array.isArray(action.Parameters) 
                ? action.Parameters.join(',') 
                : (typeof action.Parameters === 'string' ? action.Parameters : ''),
            executeDefaults: action.ExecuteDefaults === true
        };
    }
}

/**
 * Validate a species action object
 * @param {Object} action - The action object to validate
 * @returns {boolean} Whether the action is valid
 */
function validateSpeciesAction(action) {
    // Check if required fields are present and valid
    const hasValidType = action.type && typeof action.type === 'string';
    if (!hasValidType) {
        return false;
    }
    
    const isCommandValid = action.type !== 'ExecuteCommand' || 
                          (action.command && typeof action.command === 'string');
    if (!isCommandValid) {
        return false;
    }
    
    // Parameters should be either a string or an array
    const hasValidParams = 
        (typeof action.parameters === 'string') || 
        (Array.isArray(action.parameters)) ||
        (typeof action.Parameters === 'string') || 
        (Array.isArray(action.Parameters));
        
    if (!hasValidParams) {
        return false;
    }
    
    // executeDefaults should be a boolean, but we accept undefined (defaults to false)
    if (action.executeDefaults !== undefined && typeof action.executeDefaults !== 'boolean') {
        return false;
    }
    
    // ExecuteDefaults should be a boolean, but we accept undefined (defaults to false)
    if (action.ExecuteDefaults !== undefined && typeof action.ExecuteDefaults !== 'boolean') {
        return false;
    }
    
    return true;
}

// Export utilities
window.speciesUtils = {
    updateSpeciesPredictions,
    addSpeciesToList,
    removeSpeciesFromList,
    startEditSpecies,
    saveEditSpecies,
    parseSpeciesText,
    formatSpeciesAction,
    validateSpeciesAction
}; 