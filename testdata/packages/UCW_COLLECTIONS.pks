create or replace package ucw_collections as
  type t_number_tab is table of number index by pls_integer;

  function sum_range (p_from in pls_integer, p_to in pls_integer) return number;
  function top_squares (p_count in pls_integer) return varchar2;
end ucw_collections;
/
