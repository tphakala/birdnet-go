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
 * @param {number} index - The index of the species to remove
 * @param {Array} speciesList - The list to remove from
 * @param {function} setSpeciesList - Function to update the species list
 * @param {function} setHasChanges - Function to mark changes (optional)
 */
function removeSpeciesFromList(index, speciesList, setSpeciesList, setHasChanges = null) {
    // Create a new list without the species at the specified index
    const updatedList = speciesList.filter((_, i) => i !== index);
    setSpeciesList(updatedList);
    
    // Mark changes if needed
    if (setHasChanges) {
        setHasChanges(true);
    }
} 