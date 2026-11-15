#ifndef SC_PERSPECTIVE_LOCATORS_H
#define SC_PERSPECTIVE_LOCATORS_H

#define SC_CENTER_LOCATOR_LOW 0.40
#define SC_CENTER_LOCATOR_HIGH 0.60

/* Fast one-frame id grid: 4 columns x 3 rows of two-tone markers. */
#define SC_ID_GRID_COLS 4
#define SC_ID_GRID_ROWS 3
#define SC_ID_GRID_CELLS (SC_ID_GRID_COLS * SC_ID_GRID_ROWS)
/* Half extents in source-frame UV. Markers are 0.09 x 0.09 UV (172x97 px). */
#define SC_ID_GRID_HALF 0.045
#define SC_ID_GRID_INNER_HALF 0.018

enum sc_locator_layout {
    SC_LOCATORS_WIDE,
    SC_LOCATORS_COMPACT,
    SC_LOCATORS_CENTER,
    /* One-frame id-coded grid (scanner fast path). */
    SC_LOCATORS_ID_GRID,
};

struct sc_locator_grid {
    double left, right, top, bottom;
};

static inline struct sc_locator_grid
sc_locator_grid_for(enum sc_locator_layout layout) {
    if (layout == SC_LOCATORS_CENTER) {
        return (struct sc_locator_grid) {
            SC_CENTER_LOCATOR_LOW, SC_CENTER_LOCATOR_HIGH,
            SC_CENTER_LOCATOR_LOW, SC_CENTER_LOCATOR_HIGH,
        };
    }
    if (layout == SC_LOCATORS_COMPACT) {
        return (struct sc_locator_grid) {.10, .25, .25, .75};
    }
    return (struct sc_locator_grid) {.25, .75, .25, .75};
}

/* Id grid cell -> source UV centre.  Cell indices are row-major. */
static inline double
sc_id_grid_u(int cell) {
    return ((cell % SC_ID_GRID_COLS) + .5) / SC_ID_GRID_COLS;
}

static inline double
sc_id_grid_v(int cell) {
    return ((cell / SC_ID_GRID_COLS) + .5) / SC_ID_GRID_ROWS;
}

/* Palette indices: 0 red, 1 blue, 2 yellow, 3 magenta. */
static inline int
sc_id_grid_outer(int cell) {
    return cell % SC_ID_GRID_COLS;
}

static inline int
sc_id_grid_inner(int cell) {
    /* Rows are 0..2, so (row + 1) never wraps to produce an equal pair. */
    return (cell % SC_ID_GRID_COLS + cell / SC_ID_GRID_COLS + 1) % 4;
}

/* Reverse lookup: palette pair -> cell, or -1. */
static inline int
sc_id_grid_cell_for(int outer, int inner) {
    if (outer < 0 || outer > 3 || inner < 0 || inner > 3 || outer == inner) {
        return -1;
    }
    for (int cell = 0; cell < SC_ID_GRID_CELLS; ++cell) {
        if (sc_id_grid_outer(cell) == outer && sc_id_grid_inner(cell) == inner) {
            return cell;
        }
    }
    return -1;
}

#endif
