create or replace package body ucw_condcomp as

  $if dbms_db_version.ver_le_11 $then
    gc_flavour constant varchar2(20) := 'legacy';
  $elsif dbms_db_version.ver_le_19 $then
    gc_flavour constant varchar2(20) := 'classic';
  $else
    gc_flavour constant varchar2(20) := 'modern';
  $end

  function feature_name return varchar2
  is
    l_name varchar2(100);
  begin
    $if $$ucw_debug $then
      l_name := 'debug-' || gc_flavour;
    $else
      l_name := gc_flavour;
    $end
    return l_name;
  end feature_name;

  function describe_value (p_value in number) return varchar2
  is
    l_text varchar2(50);
  begin
    l_text := case
                when p_value is null then 'unknown'
                when p_value < 0 then 'negative'
                when p_value = 0 then 'zero'
                else 'positive'
              end;
    return l_text;
  end describe_value;

end ucw_condcomp;
/
